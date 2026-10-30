package main

import (
 "os"
 "path/filepath"
 "testing"
)

func TestModelConfig(t *testing.T){
 path:=filepath.Join(t.TempDir(),"config.json")
 none:=func(string)string{return ""}
 c,e:=loadModelConfig(path,true,none)
 if e!=nil || c.Model!="gpt-4.1-mini" || c.TTSModel!="gpt-4o-mini-tts" || c.Voice!="coral"{t.Fatalf("defaults: %+v %v",c,e)}
 if _,e=loadModelConfig(path,false,none);e==nil{t.Fatal("explicit missing path accepted")}
 if e=os.WriteFile(path,[]byte("\xef\xbb\xbf{\r\n\"model\":\"test-llm\",\"tts_model\":\"tts-1-hd\",\"voice\":\"nova\"}"),0600);e!=nil{t.Fatal(e)}
 c,e=loadModelConfig(path,false,none)
 if e!=nil || c.Model!="test-llm" || c.TTSModel!="tts-1-hd" || c.Voice!="nova"{t.Fatalf("file: %+v %v",c,e)}
 c,e=loadModelConfig(path,false,func(k string)string{if k=="OPENAI_VOICE"{return "cedar"};return ""})
 if e!=nil || c.Voice!="cedar" || c.Model!="test-llm"{t.Fatalf("override: %+v %v",c,e)}
 if e=os.WriteFile(path,[]byte(`{"wake_word":"Алиса"}`),0600);e!=nil{t.Fatal(e)}
 c,e=loadModelConfig(path,false,none)
 if e!=nil || c.WakeWord!="алиса"{t.Fatalf("wake word: %+v %v",c,e)}
 for _,bad:=range []string{`null`,`[]`,`{"modle":"typo"}`,`{"model":""}`,`{"voice":"two words"}`,`{"wake_word":"два слова"}`,`{} {}`,`{`} {
  if e=os.WriteFile(path,[]byte(bad),0600);e!=nil{t.Fatal(e)}
  if _,e=loadModelConfig(path,false,none);e==nil{t.Errorf("accepted %s",bad)}
 }
}

func TestSpeechModelSelection(t *testing.T){
 for _,model:=range []string{"gpt-4o-mini-tts","tts-1","tts-1-hd","tts-1-1106"}{
  a:=api{ttsModel:model,voice:"nova"}
  b:=a.speechBody("Тест")
  if b["model"]!=model || b["voice"]!="nova" || b["response_format"]!="pcm"{t.Fatal(b)}
  _,instructions:=b["instructions"]
  if instructions!=(model=="gpt-4o-mini-tts"){t.Fatalf("instructions incompatible with %s",model)}
 }
}
