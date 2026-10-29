package main

import (
 "bufio"
 "bytes"
 "context"
 "encoding/binary"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "net/http"
 "os"
 "os/exec"
 "os/signal"
 "strings"
 "syscall"
 "time"
)

const muteFile = "/tmp/mini3-openai-mute"
type api struct { key, model, voice, base string; client *http.Client }
func env(k, fallback string) string { if v:=os.Getenv(k); v!="" { return v }; return fallback }
func newAPI() (*api,error) {
 b,err:=os.ReadFile(env("OPENAI_KEY_FILE","/data/opt/openai/key.txt")); if err!=nil {return nil,fmt.Errorf("read API key: %w",err)}
 key:=strings.TrimSpace(strings.TrimPrefix(string(b),"\ufeff")); if !strings.HasPrefix(key,"sk-") || strings.ContainsAny(key,"\r\n\t ") {return nil,errors.New("invalid API key file")}
 return &api{key,env("OPENAI_MODEL","gpt-4.1-mini"),env("OPENAI_VOICE","coral"),"https://api.openai.com/v1",&http.Client{Timeout:60*time.Second,CheckRedirect:func(r *http.Request,via []*http.Request) error{return errors.New("API redirect refused")}}},nil
}
func (a *api) post(ctx context.Context,path string,body any,limit int64) ([]byte,error) {
 b,err:=json.Marshal(body); if err!=nil{return nil,err}
 req,err:=http.NewRequestWithContext(ctx,"POST",a.base+path,bytes.NewReader(b)); if err!=nil{return nil,err}
 req.Header.Set("Authorization","Bearer "+a.key); req.Header.Set("Content-Type","application/json")
 resp,err:=a.client.Do(req); if err!=nil{return nil,err}; defer resp.Body.Close()
 data,err:=io.ReadAll(io.LimitReader(resp.Body,limit+1)); if err!=nil{return nil,err}; if int64(len(data))>limit{return nil,errors.New("API response too large")}
 if resp.StatusCode<200 || resp.StatusCode>=300 {
  var e struct{ Error struct{Message string `json:"message"`; Code any `json:"code"`} `json:"error"` }; _=json.Unmarshal(data,&e)
  msg:=strings.ReplaceAll(e.Error.Message,a.key,"[redacted]"); if len(msg)>400{msg=msg[:400]}
  return nil,fmt.Errorf("OpenAI HTTP %d: %s",resp.StatusCode,msg)
 }
 return data,nil
}
func (a *api) ask(ctx context.Context,q string)(string,error) {
 if len([]rune(q))>1500{return "",errors.New("question too long")}
 data,err:=a.post(ctx,"/responses",map[string]any{"model":a.model,"store":false,"max_output_tokens":300,"instructions":"Ты — голосовой помощник в домашней колонке. Отвечай по-русски, кратко: 1–3 предложения обычным текстом без Markdown. Не утверждай, что управляешь устройствами или имеешь доступ к текущим данным: таких инструментов у тебя нет.","input":q},2<<20)
 if err!=nil{return "",err}
 var r struct{Status string `json:"status"`; Output []struct{Type string `json:"type"`; Content []struct{Type string `json:"type"`; Text string `json:"text"`; Refusal string `json:"refusal"`} `json:"content"`} `json:"output"`}
 if err=json.Unmarshal(data,&r);err!=nil{return "",err}
 var parts []string
 for _,o:=range r.Output{for _,c:=range o.Content{if c.Type=="output_text"{parts=append(parts,c.Text)};if c.Type=="refusal"{parts=append(parts,c.Refusal)}}}
 text:=strings.TrimSpace(strings.Join(parts,"\n"));if text==""{return "",fmt.Errorf("empty response (status %s)",r.Status)}
 return text,nil
}
// Speech API PCM: signed 16-bit LE, 24 kHz mono. Convert to 48 kHz stereo.
func pcm48(pcm []byte)([]byte,error){
 if len(pcm)==0 || len(pcm)%2!=0{return nil,errors.New("invalid PCM length")}
 out:=make([]byte,len(pcm)*4)
 for i:=0;i<len(pcm)/2;i++ {
  s:=int16(binary.LittleEndian.Uint16(pcm[i*2:])); next:=s
  if i+1<len(pcm)/2{next=int16(binary.LittleEndian.Uint16(pcm[(i+1)*2:]))}
  mid:=int16((int32(s)+int32(next))/2)
  for j,v:=range []int16{s,s,mid,mid}{binary.LittleEndian.PutUint16(out[i*8+j*2:],uint16(v))}
 }
 return out,nil
}
func (a *api) say(ctx context.Context,text string)error{
 if len([]rune(text))>2000{return errors.New("speech text too long")}
 data,err:=a.post(ctx,"/audio/speech",map[string]any{"model":"gpt-4o-mini-tts","voice":a.voice,"input":text,"instructions":"Говори по-русски, естественно и ясно, в умеренном темпе.","response_format":"pcm"},4<<20)
 if err!=nil{return err}; stereo,err:=pcm48(data);if err!=nil{return err}
 cmd:=exec.CommandContext(ctx,"/usr/bin/aplay","-q","-D","speakers","-t","raw","-f","S16_LE","-r","48000","-c","2")
 cmd.Stdin=bytes.NewReader(stereo);cmd.Stderr=os.Stderr
 return cmd.Run()
}
func wakeQuestion(text,wake string)(string,bool){
 words:=strings.Fields(strings.ToLower(text));for i,w:=range words{if strings.Trim(w,",.!?:;")==wake{return strings.Join(words[i+1:]," "),true}};return "",false
}
func (a *api) listen(ctx context.Context)error{
 _=os.Remove(muteFile)
 cmd:=exec.Command("/data/opt/bin/vosk-stream","/data/opt/vosk/model-ru")
 cmd.Env=append(os.Environ(),"VOSK_MUTE_FILE="+muteFile)
 stdout,err:=cmd.StdoutPipe();if err!=nil{return err};cmd.Stderr=os.Stderr
 if err=cmd.Start();err!=nil{return err}
 done:=make(chan struct{});go func(){select{case <-ctx.Done():_ =cmd.Process.Signal(syscall.SIGTERM);case <-done:}}()
 defer close(done);defer os.Remove(muteFile)
 wake:=env("OPENAI_WAKE_WORD","компьютер");fmt.Printf("Say: %s, <question>. Voice is AI-generated.\n",wake)
 scanner:=bufio.NewScanner(stdout);scanner.Buffer(make([]byte,4096),64<<10)
 var armed time.Time;var last time.Time;var calls []time.Time
 for scanner.Scan(){
  if st,e:=os.Stdout.Stat();e==nil && st.Mode().IsRegular() && st.Size()>1<<20{_ =os.Stdout.Truncate(0);_,_=os.Stdout.Seek(0,0)}
  var event struct{Event string `json:"event"`;Result struct{Text string `json:"text"`} `json:"result"`}
  if json.Unmarshal(scanner.Bytes(),&event)!=nil{continue}
  if event.Event=="ready" || event.Event=="stats"{fmt.Println(scanner.Text());continue}
  if event.Event!="final"{continue}
  text:=strings.TrimSpace(event.Result.Text);if text==""{continue}
  q,found:=wakeQuestion(text,wake)
  if found && q==""{armed=time.Now().Add(10*time.Second);fmt.Println("Listening for question (10 seconds)");continue}
  if !found{if time.Now().After(armed){continue};q=text}
  armed=time.Time{}
  if time.Since(last)<3*time.Second{continue}
  recent:=calls[:0];for _,t:=range calls{if time.Since(t)<time.Minute{recent=append(recent,t)}};calls=recent
  if len(calls)>=6{fmt.Fprintln(os.Stderr,"Rate limit: six questions per minute");continue}
  if err=os.WriteFile(muteFile,[]byte("busy\n"),0600);err!=nil{return err}
  // Capture continues but STT discards sound during requests/playback.
  fmt.Println("Question:",q);calls=append(calls,time.Now())
  answer,e:=a.ask(ctx,q)
  if e==nil{fmt.Println("Answer:",answer);e=a.say(ctx,answer)}
  if e!=nil{fmt.Fprintln(os.Stderr,"Request failed:",e)}
  timer:=time.NewTimer(700*time.Millisecond);select{case <-timer.C:case <-ctx.Done():timer.Stop()}
  _=os.Remove(muteFile);last=time.Now()
  if ctx.Err()!=nil{break}
 }
 _=cmd.Process.Signal(syscall.SIGTERM)
 waitErr:=cmd.Wait();if ctx.Err()!=nil{return nil};if scanner.Err()!=nil{return scanner.Err()};return waitErr
}
func main(){
 ctx,cancel:=signal.NotifyContext(context.Background(),os.Interrupt,syscall.SIGTERM);defer cancel()
 if len(os.Args)<2{fmt.Fprintln(os.Stderr,"Usage: mini3-openai ask|say|talk <text> | listen");os.Exit(2)}
 a,err:=newAPI();if err==nil{
  mode:=os.Args[1];text:=strings.Join(os.Args[2:]," ")
  if mode!="listen" && text==""{err=errors.New("text required")}else{switch mode{
   case "ask":var reply string;reply,err=a.ask(ctx,text);if err==nil{fmt.Println(reply)}
   case "say":err=a.say(ctx,text)
   case "talk":var reply string;reply,err=a.ask(ctx,text);if err==nil{fmt.Println(reply);err=a.say(ctx,reply)}
   case "listen":err=a.listen(ctx)
   default:err=errors.New("unknown command")
  }}
 }
 if err!=nil{fmt.Fprintln(os.Stderr,err);os.Exit(1)}
}
