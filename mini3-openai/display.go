package main

import (
 "fmt"
 "os"
 "path/filepath"
 "strconv"
 "strings"
 "time"
)

// Clock exposes four seven-segment digits A..G, indices 0..3.
// The assistant updates HH:MM itself while the stock clock process is stopped.
func volumeDigits(volume int)[4]string{
 if volume<0{volume=0};if volume>100{volume=100}
 masks:=[10]string{"ABCDEF","BC","ABDEG","ABCDG","BCFG","ACDFG","ACDEFG","ABC","ABCDEFG","ABCDFG"}
 text:=fmt.Sprintf("%4d",volume);var out [4]string
 for i,c:=range text{if c!=' '{out[i]=masks[c-'0']}}
 return out
}
func callDigits()[4]string{return [4]string{"ADEF","ABCEFG","DEF","DEF"}}
func clockDigits(now time.Time)[4]string{
 value:=now.Format("1504");var out [4]string
 masks:=[10]string{"ABCDEF","BC","ABDEG","ABCDG","BCFG","ACDFG","ACDEFG","ABC","ABCDEFG","ABCDFG"}
 for i,c:=range value{out[i]=masks[c-'0']}
 return out
}
type displayLED struct{path,triggerPath,trigger string;before,maximum int}
type volumeDisplay struct{root string;active bool;pattern [4]string;colon bool;brightnessLevel int;leds []displayLED;syncPath,syncTrigger string}
func readNumber(path string,fallback int)int{
 b,e:=os.ReadFile(path);if e!=nil{return fallback};n,e:=strconv.Atoi(strings.TrimSpace(string(b)));if e!=nil{return fallback};return n
}
func(d *volumeDisplay)showVolume(level int){
 d.show(volumeDigits(level),false)
}
func(d *volumeDisplay)showCall(){d.show(callDigits(),false)}
func(d *volumeDisplay)showClock(now time.Time){d.show(clockDigits(now),now.Second()%2==0)}
func(d *volumeDisplay)setBrightness(level int){
	if level<5{level=5};if level>255{level=255}
	if d.brightnessLevel==level{return}
	d.brightnessLevel=level
 d.pattern=[4]string{} // redraw the current digits at the new level
}
func(d *volumeDisplay)show(digits [4]string,colon bool){
 if d.brightnessLevel<=0{d.brightnessLevel=80}
 if !d.active{
  paths,_:=filepath.Glob(filepath.Join(d.root,"bergamot:white:clock*","brightness"))
  if len(paths)==0{return}
  d.leds=nil
  for _,path:=range paths{
   trigger:=filepath.Join(filepath.Dir(path),"trigger")
   led:=displayLED{path:path,triggerPath:trigger,before:readNumber(path,0),maximum:readNumber(filepath.Join(filepath.Dir(path),"max_brightness"),255)}
   led.trigger=disableTrigger(trigger);d.leds=append(d.leds,led)
  }
  d.syncPath=filepath.Join(d.root,"cucumber:sync:clock-0","trigger");d.syncTrigger=disableTrigger(d.syncPath)
  d.active=true;d.pattern=[4]string{};d.colon=false
 }
 if d.pattern==digits && d.colon==colon{return};d.pattern=digits;d.colon=colon
 for _,led:=range d.leds{
  name:=strings.TrimPrefix(filepath.Base(filepath.Dir(led.path)),"bergamot:white:clock")
  brightness:=0
  if len(name)==2 && name[0]>='A' && name[0]<='G' && name[1]>='0' && name[1]<='3'{
   if strings.ContainsRune(digits[name[1]-'0'],rune(name[0])){brightness=led.maximum*d.brightnessLevel/255}
  }else if (name=="DH" || name=="DL") && colon{
   brightness=led.maximum*d.brightnessLevel/255
  }
  _=os.WriteFile(led.path,[]byte(strconv.Itoa(brightness)+"\n"),0644)
 }
}
func(d *volumeDisplay)restore(){
 if !d.active{return}
 for _,led:=range d.leds{
  _=os.WriteFile(led.path,[]byte(strconv.Itoa(led.before)+"\n"),0644)
  if led.trigger!=""{_ =os.WriteFile(led.triggerPath,[]byte(led.trigger+"\n"),0644)}
 }
 if d.syncTrigger!=""{_ =os.WriteFile(d.syncPath,[]byte(d.syncTrigger+"\n"),0644)}
 d.active=false;d.pattern=[4]string{};d.colon=false;d.leds=nil
}
