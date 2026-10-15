<script setup lang="ts">
import {computed,ref,watch,onMounted,onBeforeUnmount} from 'vue';
import {indexAt,positionAt,peakTimes,frequencyAt,formatTime,type Action} from './timeline';
const props=defineProps<{points:Action[];time:number;duration:number;offset:number;output:number;canSeek:boolean}>();
const emit=defineEmits<{seek:[videoSeconds:number];'seek-start':[];'seek-preview':[videoSeconds:number];'seek-end':[videoSeconds:number];'seek-cancel':[]}>();
const canvas=ref<HTMLCanvasElement>(),zoom=ref(1),center=ref(0),follow=ref(true);let observer:ResizeObserver;
const peaks=computed(()=>peakTimes(props.points));
const scriptTime=computed(()=>props.time+props.offset);
const total=computed(()=>Math.max(1,props.duration+props.offset,props.points.at(-1)?.at||0));
const span=computed(()=>total.value/zoom.value);
const start=computed(()=>Math.max(0,Math.min(total.value-span.value,center.value-span.value/2)));
const position=computed(()=>positionAt(props.points,scriptTime.value));
const frequency=computed(()=>frequencyAt(peaks.value,scriptTime.value));
function draw(){
 const element=canvas.value;if(!element)return;const ctx=element.getContext('2d');if(!ctx)return;
 const width=element.clientWidth,height=element.clientHeight,dpr=window.devicePixelRatio||1;
 if(element.width!==Math.round(width*dpr)||element.height!==Math.round(height*dpr)){element.width=Math.round(width*dpr);element.height=Math.round(height*dpr);}
 ctx.setTransform(dpr,0,0,dpr,0,0);ctx.clearRect(0,0,width,height);
 const left=34,right=width-12,top=12,bottom=height-28,w=right-left,h=bottom-top;
 const x=(at:number)=>left+(at-start.value)/span.value*w,y=(pos:number)=>bottom-pos/100*h;
 ctx.font='11px Segoe UI';ctx.lineWidth=1;
 for(const value of [0,50,100]){ctx.strokeStyle='#414243';ctx.beginPath();ctx.moveTo(left,y(value));ctx.lineTo(right,y(value));ctx.stroke();ctx.fillStyle='#999999';ctx.textAlign='right';ctx.fillText(String(value),left-7,y(value)+4);}
 ctx.textAlign='center';for(let i=0;i<=4;i++){const at=start.value+span.value*i/4;ctx.fillStyle='#999999';ctx.fillText(formatTime(at),x(at),height-7);}
 if(!props.points.length){ctx.fillStyle='#999999';ctx.fillText('暂无脚本',width/2,height/2);return;}
 ctx.save();ctx.beginPath();ctx.rect(left,top,w,h);ctx.clip();
 // Keep per-pixel extrema for long, dense scripts; this preserves spikes
 // without generating a huge SVG or drawing every point on every frame.
 const first=Math.max(0,indexAt(props.points,start.value));let end=first;
 while(end<props.points.length&&props.points[end].at<=start.value+span.value)end++;
 const dense=end-first>w*3;
 ctx.strokeStyle='#5d93db';ctx.lineWidth=1.3;ctx.beginPath();
 if(dense){let column=-1,min=100,max=0;const flush=()=>{if(column<0)return;ctx.moveTo(left+column,y(min));ctx.lineTo(left+column,y(max));};for(let i=first;i<end;i++){const p=props.points[i],next=Math.floor(x(p.at)-left);if(next!==column){flush();column=next;min=p.pos;max=p.pos;}else{min=Math.min(min,p.pos);max=Math.max(max,p.pos);}}flush();}
 else {ctx.moveTo(x(start.value),y(positionAt(props.points,start.value)));for(let i=first;i<end;i++){const p=props.points[i];ctx.lineTo(x(p.at),y(p.pos));}ctx.lineTo(x(start.value+span.value),y(positionAt(props.points,start.value+span.value)));}
 ctx.stroke();
 const cursor=x(scriptTime.value);if(cursor>=left&&cursor<=right){ctx.strokeStyle='#cbba7d';ctx.lineWidth=2;ctx.beginPath();ctx.moveTo(cursor,top);ctx.lineTo(cursor,bottom);ctx.stroke();ctx.fillStyle='#cbba7d';ctx.beginPath();ctx.arc(cursor,y(position.value),3,0,Math.PI*2);ctx.fill();}
 ctx.restore();
}
let pointer:number|undefined,dragStart=0,dragSpan=0,lastSeek=0;
function pointerSeconds(event:PointerEvent){const rect=canvas.value!.getBoundingClientRect();const ratio=Math.max(0,Math.min(1,(event.clientX-rect.left-34)/Math.max(1,rect.width-46)));return Math.max(0,(dragStart+ratio*dragSpan-props.offset)/1000)}
function beginSeek(event:PointerEvent){if(!props.canSeek||event.button!==0)return;event.preventDefault();pointer=event.pointerId;dragStart=start.value;dragSpan=span.value;canvas.value!.setPointerCapture(pointer);emit('seek-start');lastSeek=pointerSeconds(event);emit('seek-preview',lastSeek)}
function moveSeek(event:PointerEvent){if(pointer!==event.pointerId)return;lastSeek=pointerSeconds(event);emit('seek-preview',lastSeek)}
function endSeek(event:PointerEvent){if(pointer!==event.pointerId)return;lastSeek=pointerSeconds(event);pointer=undefined;if(canvas.value!.hasPointerCapture(event.pointerId))canvas.value!.releasePointerCapture(event.pointerId);emit('seek-end',lastSeek);draw()}
function cancelSeek(){if(pointer===undefined)return;pointer=undefined;emit('seek-cancel');draw()}
function changeZoom(){center.value=scriptTime.value;draw()}
watch(()=>[props.time,props.offset],()=>{if(follow.value&&pointer===undefined)center.value=scriptTime.value;draw()});
watch(()=>[props.points,props.duration],()=>{center.value=scriptTime.value;zoom.value=1;draw()});
watch([zoom,center,follow],draw);
onMounted(()=>{observer=new ResizeObserver(draw);observer.observe(canvas.value!);draw()});onBeforeUnmount(()=>observer?.disconnect());
</script>
<template><section class="waveform"><div class="wave-head"><strong>脚本波形</strong><div class="wave-stats"><span>脚本位置 <b>{{position.toFixed(0)}}</b> / 100</span><span>计划档位 <b>{{output}}</b> 档</span><span>脚本频率 <b>{{frequency.toFixed(2)}}</b> Hz</span></div></div><canvas ref="canvas" @pointerdown="beginSeek" @pointermove="moveSeek" @pointerup="endSeek" @pointercancel="cancelSeek" @lostpointercapture="cancelSeek" :class="{seekable:canSeek}" aria-label="脚本动作波形"></canvas><div class="wave-controls"><span>{{formatTime(scriptTime)}} / {{formatTime(total)}}</span><label>缩放<select v-model.number="zoom" @change="changeZoom"><option :value="1">全段</option><option :value="2">2 倍</option><option :value="5">5 倍</option><option :value="10">10 倍</option><option :value="20">20 倍</option></select></label><label class="follow"><input type="checkbox" v-model="follow">跟随播放</label></div><input v-if="zoom>1&&!follow" class="pan" type="range" min="0" :max="total" v-model.number="center" aria-label="浏览波形时间范围"></section></template>
<style scoped>
.waveform{background:#131822;border:1px solid #262e3c;border-radius:12px;padding:14px 16px}.wave-head,.wave-controls{display:flex;align-items:center;justify-content:space-between;gap:16px}.wave-head strong{font-size:14px;color:#dae5f6}.wave-stats{display:flex;gap:20px;font-size:12px;color:#96a8c3}.wave-stats b{font-weight:600;color:#82e1d1;font-variant-numeric:tabular-nums}canvas{display:block;width:100%;height:120px;margin:8px 0}.seekable{cursor:crosshair;touch-action:none}.wave-controls{font-size:12px;color:#a2b2cb}.wave-controls>span{flex:1;font-variant-numeric:tabular-nums}.wave-controls label{display:flex;flex-direction:row;align-items:center;gap:8px;font-size:12px}.wave-controls select{padding:4px 8px}.follow input{margin:0;accent-color:#63dcc8}.pan{width:100%;margin-top:10px}.waveform p{font-size:11px;color:#7e8fa9;margin:9px 0 2px}.wave-controls small{color:#7e8fa9}@media(max-width:900px){.wave-stats{gap:10px}}
</style>

<style scoped>@media(min-height:950px){canvas{height:180px}}@media(max-height:720px){canvas{height:90px}.waveform{padding:10px 12px}.wave-head strong{font-size:12px}}@media(max-width:1000px){.wave-stats{gap:8px;font-size:11px}.wave-head{gap:8px}.wave-controls{gap:10px}}</style>
