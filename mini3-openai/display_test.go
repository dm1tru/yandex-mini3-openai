package main

import (
 "os"
 "path/filepath"
 "testing"
)
func TestVolumeDigits(t *testing.T){
 for _,tc:=range []struct{n int;want [4]string}{
  {0,[4]string{"","","","ABCDEF"}},
  {50,[4]string{"","","ACDFG","ABCDEF"}},
  {100,[4]string{"","BC","ABCDEF","ABCDEF"}},
  {150,[4]string{"","BC","ABCDEF","ABCDEF"}},
  {-1,[4]string{"","","","ABCDEF"}},
 }{if got:=volumeDigits(tc.n);got!=tc.want{t.Fatalf("%d: %v",tc.n,got)}}
}
func TestDisplayRestoresBrightness(t *testing.T){
 root:=t.TempDir();dir:=filepath.Join(root,"bergamot:white:clockA3");if e:=os.MkdirAll(dir,0755);e!=nil{t.Fatal(e)}
 for name,value:=range map[string]string{"brightness":"17\n","max_brightness":"255\n","trigger":"[none] timer"}{if e:=os.WriteFile(filepath.Join(dir,name),[]byte(value),0644);e!=nil{t.Fatal(e)}}
 d:=volumeDisplay{root:root};d.show(50)
 if n:=readNumber(filepath.Join(dir,"brightness"),-1);n!=80{t.Fatal("digit did not light",n)}
 d.restore();if n:=readNumber(filepath.Join(dir,"brightness"),-1);n!=17{t.Fatal("brightness not restored",n)}
 if d.active{t.Fatal("display still active")}
}
