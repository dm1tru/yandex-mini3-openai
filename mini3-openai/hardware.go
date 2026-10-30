package main

import (
 "context"
 "encoding/binary"
 "errors"
 "fmt"
 "os"
 "os/exec"
 "path/filepath"
 "regexp"
 "strconv"
 "strings"
 "sync"
 "syscall"
 "time"
)

// The deployed helper is a static aarch64 executable: evdev timeval is 16 bytes.
func volumeDelta(event []byte) int {
 if len(event)!=24 || binary.LittleEndian.Uint16(event[16:])!=1 { return 0 }
 value:=int32(binary.LittleEndian.Uint32(event[20:]))
 if value!=1 && value!=2 { return 0 } // press/repeat, never release
 switch binary.LittleEndian.Uint16(event[18:]) { case 115:return 5;case 114:return -5 }
 return 0
}

var percentPattern=regexp.MustCompile(`\[(\d+)%\]`)
func mixerPercent(output string)(int,error){
 m:=percentPattern.FindStringSubmatch(output);if m==nil{return 0,errors.New("Master volume percentage missing")}
 n,err:=strconv.Atoi(m[1]);if err!=nil || n<0 || n>100{return 0,errors.New("invalid Master volume")};return n,nil
}

type ledChannel struct { file *os.File; maximum,last int; triggerPath,trigger string }
func disableTrigger(path string)string{
 b,e:=os.ReadFile(path);if e!=nil{return ""}
 value:=string(b);start:=strings.IndexByte(value,'[');end:=strings.IndexByte(value,']')
 if start<0 || end<=start{return ""}
 previous:=value[start+1:end]
 if e=os.WriteFile(path,[]byte("none\n"),0644);e!=nil{return ""}
 return previous
}
type hardware struct {
 mu sync.Mutex
 state string
 until time.Time
 conversationMode bool
 volume int
 volumeUntil time.Time
 displayUntil time.Time
 display volumeDisplay
 leds [6][3]ledChannel
 cancel context.CancelFunc
 wg sync.WaitGroup
 syncPath,syncTrigger string
}

func startHardware(parent context.Context)*hardware{
 ctx,cancel:=context.WithCancel(parent)
 h:=&hardware{state:"loading",cancel:cancel}
 root:=env("MINI3_LED_ROOT","/sys/class/leds")
 h.display.root=root
 opened:=0
 for i:=0;i<6;i++ {for c,color:=range []string{"red","green","blue"}{
  dir:=filepath.Join(root,fmt.Sprintf("bergamot:%s:logo%d",color,i))
  ch:=&h.leds[i][c];ch.maximum=255;ch.last=-1
  if b,e:=os.ReadFile(filepath.Join(dir,"max_brightness"));e==nil{if n,e:=strconv.Atoi(strings.TrimSpace(string(b)));e==nil && n>0{ch.maximum=n}}
  f,e:=os.OpenFile(filepath.Join(dir,"brightness"),os.O_WRONLY,0);if e!=nil{continue};ch.file=f;opened++
  ch.triggerPath=filepath.Join(dir,"trigger");ch.trigger=disableTrigger(ch.triggerPath)
 }}
 h.syncPath=filepath.Join(root,"bergamot:logo:sync","trigger");h.syncTrigger=disableTrigger(h.syncPath)
 fmt.Fprintf(os.Stderr,"Hardware: %d/18 LED channels\n",opened)
 h.wg.Add(2);go h.animate(ctx);go h.buttons(ctx)
 return h
}
func(h *hardware)set(state string,duration time.Duration){
 h.mu.Lock();defer h.mu.Unlock()
 if h.state!=state{fmt.Fprintln(os.Stderr,"Indicator:",state)}
 h.state=state;h.until=time.Time{}
 if duration>0{h.until=time.Now().Add(duration)}
}
func(h *hardware)setConversationMode(active bool){
 h.mu.Lock();defer h.mu.Unlock()
 h.conversationMode=active
}
func(h *hardware)close(){
 h.cancel();h.wg.Wait();h.draw([6][3]int{})
 h.display.restore()
 for i:=range h.leds{for c:=range h.leds[i]{ch:=&h.leds[i][c];if ch.file!=nil{ch.file.Close()};if ch.trigger!=""{_ =os.WriteFile(ch.triggerPath,[]byte(ch.trigger+"\n"),0644)}}}
 if h.syncTrigger!=""{_ =os.WriteFile(h.syncPath,[]byte(h.syncTrigger+"\n"),0644)}
}

// Values are normalized 0..255 and capped to a modest peak of 70.
func ringFrame(state string,tick int,volume int,overlay bool)[6][3]int{
 var frame [6][3]int
 if overlay{
  lit:=(volume*6+99)/100
  for i:=0;i<lit;i++{frame[i]=[3]int{0,55,25}}
  if volume==0{frame[0]=[3]int{25,0,0}}
  return frame
 }
 pulse:=tick%20;if pulse>10{pulse=20-pulse};pulse=15+pulse*5
 for i:=range frame{switch state{
  case "loading":frame[i]=[3]int{pulse/3,pulse/3,pulse/3}
  case "listening":frame[i]=[3]int{0,40,65}
  case "thinking":frame[i]=[3]int{pulse,pulse/3,0}
  case "speaking":frame[i]=[3]int{0,55,12}
  case "error":frame[i]=[3]int{60,0,0}
 }}
 return frame
}
func(h *hardware)draw(frame [6][3]int){
 for i:=range h.leds{for c:=range h.leds[i]{
  ch:=&h.leds[i][c];if ch.file==nil{continue}
  value:=frame[i][c]*ch.maximum/255;if value==ch.last{continue}
  if _,err:=ch.file.Seek(0,0);err!=nil{continue}
  if _,err:=fmt.Fprintf(ch.file,"%d\n",value);err==nil{ch.last=value}
 }}
}
func(h *hardware)animate(ctx context.Context){
 defer h.wg.Done();ticker:=time.NewTicker(100*time.Millisecond);defer ticker.Stop();tick:=0
 for{select{case <-ctx.Done():return;case now:=<-ticker.C:
  h.mu.Lock()
  if !h.until.IsZero() && now.After(h.until){h.state="idle";h.until=time.Time{}}
  frame:=ringFrame(h.state,tick,h.volume,now.Before(h.volumeUntil))
  showVolume:=now.Before(h.displayUntil);level:=h.volume;conversationMode:=h.conversationMode;h.mu.Unlock()
  if showVolume{h.display.showVolume(level)}else if conversationMode{h.display.showCall()}else{h.display.restore()}
  h.draw(frame);tick++
 }}
}
func buttonDevice()string{
 if path:=os.Getenv("MINI3_INPUT_DEVICE");path!=""{return path}
 names,_:=filepath.Glob("/sys/class/input/event*/device/name")
 for _,name:=range names{b,e:=os.ReadFile(name);if e==nil && strings.Contains(strings.ToLower(string(b)),"aw933"){
  return filepath.Join("/dev/input",filepath.Base(filepath.Dir(filepath.Dir(name))))
 }}
 return "/dev/input/event1"
}
func(h *hardware)buttons(ctx context.Context){
 defer h.wg.Done()
 path:=buttonDevice();fd,err:=syscall.Open(path,syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC,0)
 if err!=nil{fmt.Fprintln(os.Stderr,"Volume buttons:",err);return};defer syscall.Close(fd)
 fmt.Fprintln(os.Stderr,"Volume buttons:",path)
 ticker:=time.NewTicker(25*time.Millisecond);defer ticker.Stop()
 var last time.Time;buf:=make([]byte,24*32)
 for{select{case <-ctx.Done():return;case <-ticker.C:
  n,err:=syscall.Read(fd,buf)
  if err==syscall.EAGAIN || err==syscall.EINTR{continue}
  if err!=nil{fmt.Fprintln(os.Stderr,"Volume input:",err);return}
  for off:=0;off+24<=n;off+=24{
   delta:=volumeDelta(buf[off:off+24]);if delta==0{continue}
   repeat:=binary.LittleEndian.Uint32(buf[off+20:])==2
   if repeat && time.Since(last)<150*time.Millisecond{continue}
   value:="5%+";if delta<0{value="5%-"}
   callCtx,cancel:=context.WithTimeout(ctx,2*time.Second)
   out,e:=exec.CommandContext(callCtx,"amixer","sset","Master",value).CombinedOutput();cancel()
   if e!=nil{fmt.Fprintln(os.Stderr,"Volume change failed:",e);continue}
   level,e:=mixerPercent(string(out));if e!=nil{fmt.Fprintln(os.Stderr,e);continue}
   h.mu.Lock();h.volume=level;h.volumeUntil=time.Now().Add(time.Second);h.displayUntil=time.Now().Add(2*time.Second);h.mu.Unlock()
   fmt.Fprintf(os.Stderr,"Volume: %d%%\n",level);last=time.Now()
  }
 }}
}
