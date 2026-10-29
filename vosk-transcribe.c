#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
#include <math.h>
#include <time.h>
#include <sys/resource.h>
#include "vosk_api.h"

static uint16_t u16(const unsigned char *p) { return p[0] | ((uint16_t)p[1]<<8); }
static uint32_t u32(const unsigned char *p) { return u16(p) | ((uint32_t)u16(p+2)<<16); }
static double seconds(void) { struct timespec t; clock_gettime(CLOCK_MONOTONIC,&t); return t.tv_sec+t.tv_nsec/1e9; }
int main(int argc, char **argv) {
    if (argc != 3) { fprintf(stderr,"Usage: %s MODEL PCM16_WAV\n",argv[0]); return 2; }
    FILE *f=fopen(argv[2],"rb"); if (!f) { perror("WAV"); return 1; }
    unsigned char h[16]; uint32_t bytes=0,rate=0; unsigned channels=0,align=0;
    if (fread(h,1,12,f)!=12 || memcmp(h,"RIFF",4) || memcmp(h+8,"WAVE",4)) {
        fprintf(stderr,"Not a RIFF WAV\n"); fclose(f); return 1;
    }
    while (fread(h,1,8,f)==8) {
        uint32_t n=u32(h+4);
        if (!memcmp(h,"fmt ",4)) {
            if (n<16 || fread(h,1,16,f)!=16 || u16(h)!=1 || u16(h+14)!=16) {
                fprintf(stderr,"Expected PCM16 WAV\n"); fclose(f); return 1;
            }
            channels=u16(h+2); rate=u32(h+4); align=u16(h+12);
            if (fseek(f,(long)(n-16+(n&1)),SEEK_CUR)) return 1;
        } else if (!memcmp(h,"data",4)) { bytes=n; break; }
        else if (n>0x7fffffffU || fseek(f,(long)(n+(n&1)),SEEK_CUR)) return 1;
    }
    if (!bytes || channels<1 || channels>8 || align!=2*channels || (rate!=16000 && rate!=48000) || bytes%align) {
        fprintf(stderr,"Expected 16/48 kHz PCM16, 1..8 channels\n"); fclose(f); return 1;
    }
    /* Select physical microphone 0; omit the observed startup transient. */
    uint32_t skip=(channels>1 ? rate : 0);
    if ((uint64_t)skip*align>=bytes) { fprintf(stderr,"Recording too short\n"); fclose(f); return 1; }
    if (fseek(f,(long)skip*align,SEEK_CUR)) return 1;
    bytes-=skip*align;
    fprintf(stderr,"Input: %u Hz, %u channels; channel 0, skip %.1fs\n",rate,channels,(double)skip/rate);
    vosk_set_log_level(-1);
    double start=seconds();
    VoskModel *model=vosk_model_new(argv[1]);
    if (!model) { fprintf(stderr,"Model load failed\n"); fclose(f); return 1; }
    VoskRecognizer *rec=vosk_recognizer_new(model,16000.0f);
    if (!rec) { vosk_model_free(model); fclose(f); return 1; }
    double loaded=seconds();
    /* 63-tap low-pass, 7 kHz cutoff, before decimation 48 -> 16 kHz. */
    double coeff[63],hist[63]={0},sum=0; unsigned pos=0,phase=0;
    for (int i=0;i<63;i++) {
        double m=i-31,fc=7000.0/48000.0;
        coeff[i]=(m==0 ? 2*fc : sin(2*M_PI*fc*m)/(M_PI*m))*(0.54-0.46*cos(2*M_PI*i/62)); sum+=coeff[i];
    }
    for (int i=0;i<63;i++) coeff[i]/=sum;
    short out[1600]; unsigned used=0; unsigned char frame[16];
    while (bytes>=align) {
        if (fread(frame,1,align,f)!=align) { fprintf(stderr,"Truncated WAV\n"); vosk_recognizer_free(rec); vosk_model_free(model); fclose(f); return 1; }
        bytes-=align; double v=(int16_t)u16(frame);
        if (rate==48000) {
            hist[pos]=v; pos=(pos+1)%63;
            if (++phase<3) continue;
            phase=0; v=0;
            for (unsigned i=0;i<63;i++) v+=coeff[i]*hist[(pos+62-i)%63];
        }
        if (v>32767) v=32767; if (v< -32768) v=-32768;
        out[used++]=(short)lrint(v);
        if (used==1600) {
            int result=vosk_recognizer_accept_waveform_s(rec,out,used);
            if (result<0) { fprintf(stderr,"Recognition error\n"); return 1; }
            if (result) { puts(vosk_recognizer_result(rec)); fflush(stdout); }
            used=0;
        }
    }
    if (used && vosk_recognizer_accept_waveform_s(rec,out,used)<0) return 1;
    puts(vosk_recognizer_final_result(rec));
    struct rusage usage; getrusage(RUSAGE_SELF,&usage);
    fprintf(stderr,"Model load %.2fs; recognition %.2fs; peak RSS %ld KiB\n",loaded-start,seconds()-loaded,usage.ru_maxrss);
    vosk_recognizer_free(rec); vosk_model_free(model); fclose(f); return 0;
}
