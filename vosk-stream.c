#define _POSIX_C_SOURCE 200809L
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <unistd.h>
#include <signal.h>
#include <pthread.h>
#include <time.h>
#include <math.h>
#include <sys/wait.h>
#include <sys/stat.h>
#include <malloc.h>
#include "vosk_api.h"

#define RATE 16000
#define BLOCK 1600
#define CAP (RATE*30)
static short ring[CAP];
static unsigned head, count;
static int eof, failed, overflow;
static volatile sig_atomic_t stopping;
static pthread_mutex_t mutex=PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t available=PTHREAD_COND_INITIALIZER;
static FILE *input;
static int replay;
static double began;
static short preroll[RATE*2];
static unsigned pre_count;
static const char *wake, *grammar;
static VoskRecognizer *replace_rec(VoskRecognizer *old,VoskModel *model,int full) {
    if(old) vosk_recognizer_free(old);
    malloc_trim(0);
    return grammar && !full ? vosk_recognizer_new_grm(model,RATE,grammar) : vosk_recognizer_new(model,RATE);
}
static int only_wake(const char *json) {
    const char *p=strchr(json,':');
    if(!p || !(p=strchr(p,'"'))) return 0;
    p++;
    return wake && !strncmp(p,wake,strlen(wake)) && p[strlen(wake)]=='"';
}
static void mode_event(int full) { printf("{\"event\":\"mode\",\"mode\":\"%s\"}\n",full?"listening":"wake"); }
static double now(void) { struct timespec t; clock_gettime(CLOCK_MONOTONIC,&t); return t.tv_sec+t.tv_nsec/1e9; }
static void stop(int sig) { (void)sig; stopping=1; }
static void bound_log(void) {
    struct stat s;
    if(!fstat(STDOUT_FILENO,&s) && S_ISREG(s.st_mode) && s.st_size>1024*1024) {
        fflush(stdout);
        if(!ftruncate(STDOUT_FILENO,0)) lseek(STDOUT_FILENO,0,SEEK_SET);
    }
}
static void enqueue(short *samples, unsigned n) {
    pthread_mutex_lock(&mutex);
    if (CAP-count<n) { overflow=1; failed=1; eof=1; }
    else {
        for(unsigned i=0;i<n;i++) ring[(head+count+i)%CAP]=samples[i];
        count+=n;
    }
    pthread_cond_signal(&available);
    pthread_mutex_unlock(&mutex);
}
static void *capture(void *unused) {
    (void)unused;
    short out[BLOCK]; unsigned used=0;
    float coeff[63],history[63]={0},sum=0;
    unsigned pos=0,phase=0,skip=48000;
    for(int i=0;i<63;i++) {
        double m=i-31,fc=7000.0/48000.0;
        coeff[i]=(m==0?2*fc:sin(2*3.141592653589793*fc*m)/(3.141592653589793*m))*(0.54-0.46*cos(2*3.141592653589793*i/62));
        sum+=coeff[i];
    }
    for(int i=0;i<63;i++) coeff[i]/=sum;
    unsigned char chunk[16384]; size_t offset=0,got=0; double deadline=now();
    while(!stopping) {
        size_t size=replay?2:16;
        if(offset+size>got) {
            got=fread(chunk,1,sizeof(chunk),input); offset=0;
            if(got<size) break;
        }
        unsigned char *frame=chunk+offset; offset+=size;
        float v=(int16_t)(frame[0]|((unsigned)frame[1]<<8));
        if(!replay) {
            if(skip) { skip--; continue; }
            history[pos]=v; pos=(pos+1)%63;
            if(++phase<3) continue;
            phase=0; v=0;
            for(unsigned i=0;i<63;i++) v+=coeff[i]*history[(pos+62-i)%63];
        }
        if(v>32767) v=32767;
        if(v< -32768) v=-32768;
        out[used++]=(short)lrintf(v);
        if(used==BLOCK) {
            if(replay) {
                deadline+=0.1;
                double delay=deadline-now();
                if(delay>0) { struct timespec t={(time_t)delay,(long)((delay-floor(delay))*1e9)}; nanosleep(&t,NULL); }
            }
            enqueue(out,used); used=0;
            pthread_mutex_lock(&mutex); int bad=failed; pthread_mutex_unlock(&mutex);
            if(bad) break;
        }
    }
    if(used) enqueue(out,used);
    pthread_mutex_lock(&mutex);
    if(ferror(input)) failed=1;
    eof=1; pthread_cond_signal(&available);
    pthread_mutex_unlock(&mutex);
    return NULL;
}
static void emit(const char *type,const char *json,double lag) {
    if(strstr(json,"\"text\" : \"\"") || strstr(json,"\"partial\" : \"\"")) return;
    bound_log();
    printf("{\"event\":\"%s\",\"elapsed_s\":%.2f,\"backlog_s\":%.2f,\"result\":",type,now()-began,lag);
    for(const char *p=json;*p;p++) if(*p!='\n' && *p!='\r') putchar(*p);
    puts("}");
}
int main(int argc,char **argv) {
    if(argc!=2 && argc!=3) { fprintf(stderr,"Usage: vosk-stream MODEL [16k-mono-s16le.pcm]\n"); return 2; }
    setvbuf(stdout,NULL,_IOLBF,0);
    signal(SIGINT,stop); signal(SIGTERM,stop);
    replay=argc==3; began=now();
    vosk_set_log_level(-1);
    VoskModel *model=vosk_model_new(argv[1]);
    if(!model) return 1;
    wake=getenv("VOSK_WAKE_WORD"); grammar=getenv("VOSK_WAKE_GRAMMAR");
    if(!wake || !*wake || !grammar || !*grammar) { wake=NULL; grammar=NULL; }
    int full=grammar?0:1;
    VoskRecognizer *rec=replace_rec(NULL,model,full);
    if(!rec) { vosk_model_free(model); return 1; }
    pid_t child=-1;
    if(replay) input=fopen(argv[2],"rb");
    else {
        int fd[2]; if(pipe(fd)) return 1;
        child=fork();
        if(child==0) {
            dup2(fd[1],STDOUT_FILENO); close(fd[0]); close(fd[1]);
            execl("/usr/bin/arecord","arecord","-q","-D","hw:0,1","-t","raw","-f","S16_LE","-r","48000","-c","8","-B","1000000","-F","20000",(char*)NULL);
            _exit(127);
        }
        close(fd[1]);
        if(child<0) { close(fd[0]); return 1; }
        input=fdopen(fd[0],"rb");
    }
    if(!input) { perror("audio input"); return 1; }
    pthread_t thread;
    if(pthread_create(&thread,NULL,capture,NULL)) { if(child>0) kill(child,SIGTERM); return 1; }
    printf("{\"event\":\"ready\",\"model_load_s\":%.2f,\"warmup_s\":%d}\n",now()-began,replay?0:1);
    mode_event(full);
    short samples[BLOCK]; double work=0,audio=0,last_report=now(),last_partial=0;
    double utterance_audio=0;
    unsigned wake_hits=0;
    const char *mute_path=getenv("VOSK_MUTE_FILE"); int was_muted=0;
    char previous[8192]="";
    while(!stopping) {
        pthread_mutex_lock(&mutex);
        while(!count && !eof && !stopping) {
            struct timespec until; clock_gettime(CLOCK_REALTIME,&until); until.tv_sec++;
            pthread_cond_timedwait(&available,&mutex,&until);
        }
        unsigned n=count>BLOCK?BLOCK:count;
        for(unsigned i=0;i<n;i++) samples[i]=ring[(head+i)%CAP];
        head=(head+n)%CAP; count-=n;
        double lag=(double)count/RATE;
        int done=eof,bad=failed;
        pthread_mutex_unlock(&mutex);
        if(bad) break;
        if(!n) { if(done) break; else continue; }
        int muted=mute_path && access(mute_path,F_OK)==0;
        if(muted || was_muted) {
            if(muted!=was_muted) {
                full=grammar?0:1; rec=replace_rec(rec,model,full);
                if(!rec) { failed=1; break; }
                previous[0]=0; utterance_audio=0; pre_count=0; wake_hits=0; mode_event(full);
            }
            pthread_mutex_lock(&mutex); head=0; count=0; pthread_mutex_unlock(&mutex);
            was_muted=muted;
            continue;
        }
        double t=now();
        if(grammar && !full) {
            if(pre_count+n>RATE*2) {
                unsigned drop=pre_count+n-RATE*2;
                memmove(preroll,preroll+drop,(pre_count-drop)*sizeof(short)); pre_count-=drop;
            }
            memcpy(preroll+pre_count,samples,n*sizeof(short)); pre_count+=n;
        }
        int result=vosk_recognizer_accept_waveform_s(rec,samples,n);
        work+=now()-t; audio+=(double)n/RATE;
        utterance_audio+=(double)n/RATE;
        if(result<0) { pthread_mutex_lock(&mutex); failed=1; pthread_mutex_unlock(&mutex); break; }
        if(grammar && !full) {
            const char *hyp=result?vosk_recognizer_result(rec):vosk_recognizer_partial_result(rec);
            wake_hits=only_wake(hyp)?wake_hits+1:0;
            if(wake_hits>=3) {
                full=1; mode_event(full); rec=replace_rec(rec,model,1);
                if(!rec) { failed=1; break; }
                if(vosk_recognizer_accept_waveform_s(rec,preroll,pre_count)<0) { failed=1; break; }
                utterance_audio=0; pre_count=0; previous[0]=0; wake_hits=0;
            } else if(utterance_audio>=10) {
                rec=replace_rec(rec,model,0);
                if(!rec) { failed=1; break; }
                /* Retain word boundaries across periodic decoder recreation. */
                unsigned keep=pre_count<RATE/2?pre_count:RATE/2;
                if(vosk_recognizer_accept_waveform_s(rec,preroll+pre_count-keep,keep)<0) { failed=1; break; }
                utterance_audio=0; wake_hits=0;
            }
        } else if(result || utterance_audio>=15) {
            const char *hyp=result?vosk_recognizer_result(rec):vosk_recognizer_final_result(rec);
            int standalone=only_wake(hyp) && utterance_audio<15;
            emit("final",hyp,lag);
            full=(!grammar || standalone)?1:0;
            rec=replace_rec(rec,model,full);
            if(!rec) { failed=1; break; }
            previous[0]=0; utterance_audio=0; pre_count=0; wake_hits=0; mode_event(full);
        }
        else if(now()-last_partial>=0.5) {
            const char *partial=vosk_recognizer_partial_result(rec);
            if(strcmp(previous,partial)) { emit("partial",partial,lag); snprintf(previous,sizeof(previous),"%s",partial); }
            last_partial=now();
        }
        if(now()-last_report>=30) {
            bound_log();
            printf("{\"event\":\"stats\",\"audio_s\":%.1f,\"compute_ratio\":%.2f,\"backlog_s\":%.2f}\n",audio,work/audio,lag);
            last_report=now();
        }
    }
    stopping=1;
    if(child>0) kill(child,SIGTERM);
    pthread_join(thread,NULL); fclose(input);
    int child_status=0;
    if(child>0) { waitpid(child,&child_status,0); if(WIFEXITED(child_status) && WEXITSTATUS(child_status)!=0) failed=1; }
    if(overflow) fprintf(stderr,"Audio backlog exceeded 30s; stopping instead of silently dropping speech.\n");
    /* Shutdown must not turn a partial recording into a new API request. */
    printf("{\"event\":\"stopped\",\"audio_s\":%.1f,\"compute_ratio\":%.2f,\"error\":%s}\n",audio,audio?work/audio:0,failed?"true":"false");
    vosk_recognizer_free(rec); vosk_model_free(model);
    return failed?1:0;
}
