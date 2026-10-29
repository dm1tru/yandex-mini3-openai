// Download only the two build/runtime files from the checksum-pinned upstream ZIP.
package main

import (
 "archive/zip"
 "bytes"
 "crypto/sha256"
 "fmt"
 "io"
 "net/http"
 "os"
 "time"
)
func main() {
 client:=http.Client{Timeout:120*time.Second}
 r,err:=client.Get("https://github.com/alphacep/vosk-api/releases/download/v0.3.45/vosk-linux-armv7l-0.3.45.zip")
 if err!=nil{panic(err)};defer r.Body.Close()
 if r.StatusCode!=200{panic(r.Status)}
 data,err:=io.ReadAll(io.LimitReader(r.Body,10<<20));if err!=nil{panic(err)}
 if fmt.Sprintf("%x",sha256.Sum256(data))!="10b795ae478ef1d530fcbfbbea9ccbbbf3b7e7c244bd5fd3176f4a6af32f3c8c"{panic("Vosk checksum mismatch")}
 z,err:=zip.NewReader(bytes.NewReader(data),int64(len(data)));if err!=nil{panic(err)}
 if err=os.MkdirAll("/vosk",0755);err!=nil{panic(err)}
 for _,name:=range []string{"libvosk.so","vosk_api.h"}{
  found:=false
  for _,f:=range z.File{if f.Name=="vosk-linux-armv7l-0.3.45/"+name{
   in,e:=f.Open();if e!=nil{panic(e)}
   b,e:=io.ReadAll(io.LimitReader(in,20<<20));in.Close();if e!=nil{panic(e)}
   if e=os.WriteFile("/vosk/"+name,b,0644);e!=nil{panic(e)};found=true;break
  }}
  if !found{panic("Missing "+name)}
 }
}
