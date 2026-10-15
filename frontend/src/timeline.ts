export type Action = {at:number;pos:number};
export function indexAt(points:Action[],ms:number):number {
  let lo=0,hi=points.length;
  while(lo<hi){const mid=(lo+hi)>>>1;if(points[mid].at<=ms)lo=mid+1;else hi=mid;}
  return lo-1;
}
export function positionAt(points:Action[],ms:number):number {
  const i=indexAt(points,ms);if(i<0)return 0;
  const a=points[i],b=points[i+1];if(!b||b.at===a.at)return a.pos;
  return a.pos+(b.pos-a.pos)*(ms-a.at)/(b.at-a.at);
}
export function levelAt(points:Action[],ms:number):number {
  const i=indexAt(points,ms);if(i<0||ms>points[points.length-1].at)return 0;
  return Math.ceil(points[i].pos/10);
}
// A full cycle spans two successive upper turning points. Flat segments do
// not create extra peaks. Values are script estimates, not motor telemetry.
export function peakTimes(points:Action[]):number[] {
  const peaks:number[]=[];let direction=0;
  for(let i=1;i<points.length;i++){
    const next=Math.sign(points[i].pos-points[i-1].pos);
    if(next===0)continue;
    if(direction>0&&next<0)peaks.push(points[i-1].at);
    direction=next;
  }
  return peaks;
}
export function frequencyAt(peaks:number[],ms:number):number {
  if(peaks.length<2||ms<peaks[0]||ms>=peaks[peaks.length-1])return 0;
  let lo=0,hi=peaks.length;while(lo<hi){const mid=(lo+hi)>>>1;if(peaks[mid]<=ms)lo=mid+1;else hi=mid;}
  const span=peaks[lo]-peaks[lo-1];return span>0?1000/span:0;
}
export function formatTime(ms:number):string {
  const sec=Math.max(0,Math.floor(ms/1000));const h=Math.floor(sec/3600),m=Math.floor(sec/60)%60,s=sec%60;
  return (h?`${h}:`:'')+`${h?String(m).padStart(2,'0'):m}:${String(s).padStart(2,'0')}`;
}
export const cycleMS=[0,10000/14.5,10000/17.5,10000/21.5,10000/14.5,10000/22,10000/26.5,10000/28.5,10000/30.5,10000/32.5,10000/34.5];
export function cyclePlan(points:Action[]):{start:number;end:number}[]{
  const turns:number[]=[];let direction=0;
  for(let i=1;i<points.length;i++){const next=Math.sign(points[i].pos-points[i-1].pos);if(!next)continue;if(!direction||next!==direction)turns.push(points[i-1].at);direction=next;}
  if(direction)turns.push(points[points.length-1].at);
  const result:{start:number;end:number}[]=[];for(let i=0;i+2<turns.length;i+=2){if(turns[i+2]>turns[i])result.push({start:turns[i],end:turns[i+2]});}return result;
}
export function cycleLevel(span:number,cap:number):number{
  cap=Math.min(10,cap);if(cap<=0)return 0;for(let l=1;l<=cap;l++){if(l===4)continue;if(cycleMS[l]<=span)return l;}return cap===4?3:cap;
}
