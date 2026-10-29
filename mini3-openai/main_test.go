package main

import (
 "context"
 "encoding/binary"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
)
func TestResponsesAndAuthorization(t *testing.T){
 s:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
  if r.URL.Path!="/responses" || r.Header.Get("Authorization")!="Bearer test-secret" {t.Error("bad request")}
  w.Header().Set("Content-Type","application/json")
  _,_=w.Write([]byte(`{"status":"completed","output":[{"type":"reasoning"},{"type":"message","content":[{"type":"output_text","text":"Привет"},{"type":"output_text","text":"мир"}]}]}`))
 }));defer s.Close()
 a:=api{key:"test-secret",model:"test-model",base:s.URL,client:s.Client()}
 text,err:=a.ask(context.Background(),"test");if err!=nil || text!="Привет\nмир"{t.Fatalf("%q %v",text,err)}
}
func TestErrorRedactsKey(t *testing.T){
 s:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){w.WriteHeader(401);_,_=w.Write([]byte(`{"error":{"message":"wrong test-secret"}}`))}));defer s.Close()
 a:=api{key:"test-secret",base:s.URL,client:s.Client()};_,err:=a.ask(context.Background(),"test")
 if err==nil || strings.Contains(err.Error(),"test-secret"){t.Fatal("secret exposed or error missing")}
}
func TestPCMConversion(t *testing.T){
 out,err:=pcm48([]byte{0xe8,3,0x18,0xfc});if err!=nil || len(out)!=16{t.Fatal(err,len(out))}
 want:=[]int16{1000,1000,0,0,-1000,-1000,-1000,-1000}
 for i,v:=range want{if int16(binary.LittleEndian.Uint16(out[i*2:]))!=v{t.Fatal("incorrect sample",i)}}
 if _,err=pcm48([]byte{1});err==nil{t.Fatal("odd PCM accepted")}
}
func TestWakeWordBoundary(t *testing.T){
 if _,ok:=wakeQuestion("компьютерный стол","компьютер");ok{t.Fatal("substring activated wake")}
 if q,ok:=wakeQuestion("Компьютер, какая погода","компьютер");!ok || q!="какая погода"{t.Fatal(q,ok)}
}
