package main

import (
 "encoding/binary"
 "testing"
)
func TestVolumeEvents(t *testing.T){
 for _,tc:=range []struct{kind,code uint16;value int32;want int}{
  {1,115,1,5},{1,114,1,-5},{1,115,2,5},{1,114,0,0},{0,115,1,0},{1,116,1,0},
 }{
  b:=make([]byte,24);binary.LittleEndian.PutUint16(b[16:],tc.kind);binary.LittleEndian.PutUint16(b[18:],tc.code);binary.LittleEndian.PutUint32(b[20:],uint32(tc.value))
  if got:=volumeDelta(b);got!=tc.want{t.Fatalf("event %+v: got %d",tc,got)}
 }
 if volumeDelta(make([]byte,16))!=0{t.Fatal("accepted armhf event layout in arm64 helper")}
}
func TestMixerPercent(t *testing.T){
 n,e:=mixerPercent("Mono: Playback 128 [50%]");if e!=nil || n!=50{t.Fatal(n,e)}
 if _,e=mixerPercent("error");e==nil{t.Fatal("missing volume accepted")}
 if _,e=mixerPercent("[150%]");e==nil{t.Fatal("invalid volume accepted")}
}
func TestRingStates(t *testing.T){
 if ringFrame("idle",0,50,false)!=([6][3]int{}){t.Fatal("idle should be dark")}
 if ringFrame("listening",0,0,false)[0]!=([3]int{0,40,65}){t.Fatal("wake indicator missing")}
 for _,state:=range []string{"loading","listening","thinking","speaking","error"}{
  for tick:=0;tick<40;tick++{for _,pixel:=range ringFrame(state,tick,0,false){for _,v:=range pixel{if v<0 || v>70{t.Fatal("brightness out of range")}}}}
 }
 frame:=ringFrame("thinking",0,50,true)
 if frame[2]==([3]int{}) || frame[3]!=([3]int{}){t.Fatal("volume overlay should show three of six LEDs at 50%")}
}
