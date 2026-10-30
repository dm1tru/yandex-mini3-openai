package main

import (
 "bytes"
 "encoding/json"
 "errors"
 "fmt"
 "io"
 "os"
 "strings"
 "unicode"
)

const defaultConfigPath = "/data/opt/openai/config.json"
type modelConfig struct {
 Model string `json:"model"`
 RealtimeModel string `json:"realtime_model"`
 TTSModel string `json:"tts_model"`
 Voice string `json:"voice"`
 WakeWord string `json:"wake_word"`
}

func loadModelConfig(path string, optional bool, getenv func(string) string) (modelConfig,error) {
 c:=modelConfig{"gpt-4.1-mini","gpt-realtime-2.1","gpt-4o-mini-tts","coral","компьютер"}
 b,err:=os.ReadFile(path)
 if err!=nil && !(optional && errors.Is(err,os.ErrNotExist)){return c,fmt.Errorf("read model config: %w",err)}
 if err==nil {
  d:=json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(b,[]byte{0xef,0xbb,0xbf})))
  d.DisallowUnknownFields()
  // Decode into a pointer so JSON null is rejected rather than silently ignored.
  p:=&c
  if err=d.Decode(&p);err!=nil{return c,errors.New("invalid model config JSON or unknown field")}
  if p==nil{return c,errors.New("model config must be an object")}
  var extra any
  if d.Decode(&extra)!=io.EOF{return c,errors.New("unexpected data after model config")}
 }
 for _,v:=range []struct{key string; dst *string}{{"OPENAI_MODEL",&c.Model},{"OPENAI_REALTIME_MODEL",&c.RealtimeModel},{"OPENAI_TTS_MODEL",&c.TTSModel},{"OPENAI_VOICE",&c.Voice},{"OPENAI_WAKE_WORD",&c.WakeWord}} {
  if s:=getenv(v.key);s!=""{*v.dst=s}
  *v.dst=strings.TrimSpace(*v.dst)
  if *v.dst=="" || strings.ContainsAny(*v.dst,"\r\n\t "){return c,fmt.Errorf("invalid value for %s",v.key)}
 }
 c.WakeWord=strings.ToLower(c.WakeWord)
 for _,r:=range c.WakeWord{if !unicode.IsLetter(r){return c,errors.New("wake_word must be one word containing letters only")}}
 return c,nil
}
func currentModelConfig()(modelConfig,error){
 path:=os.Getenv("OPENAI_CONFIG_FILE")
 optional:=path==""
 if optional{path=defaultConfigPath}
 return loadModelConfig(path,optional,os.Getenv)
}
func (a *api) speechBody(text string) map[string]any {
 model:=a.ttsModel
 if model==""{model="gpt-4o-mini-tts"}
 body:=map[string]any{"model":model,"voice":a.voice,"input":text,"response_format":"pcm"}
 if model!="tts-1" && model!="tts-1-hd" && !strings.HasPrefix(model,"tts-1-") {
  body["instructions"]="Говори по-русски, естественно и ясно, в умеренном темпе."
 }
 return body
}
