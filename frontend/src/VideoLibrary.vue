<script setup lang="ts">
import {ref,computed,onMounted,onBeforeUnmount,watch} from 'vue';
import HoverText from './HoverText.vue';
const props=defineProps<{busy:boolean;selected:string;audioAllowed:boolean}>();
const emit=defineEmits<{select:[id:string];expand:[expanded:boolean];queue:[ids:string[]]}>();
type Entry={id:string;name:string;folder:string;kind:string;has_script:boolean};
type State={directory:string;items:Entry[];skipped:number;scanning?:boolean;directories?:number;found?:number};
type API={LibraryState:()=>Promise<State>;ScanLibrary:(choose:boolean)=>Promise<State|null>;CancelLibraryScan:()=>Promise<void>};
const api=()=>(window as unknown as {go:{main:{App:API}}}).go.main.App;
const directory=ref(''),items=ref<Entry[]>([]),query=ref(''),scanning=ref(false),scanPending=ref(false),message=ref(''),limit=ref(100);let generation=0;
const scannedDirectories=ref(0),foundMedia=ref(0);let progressTimer:number|undefined,progressReading=false;
async function readProgress(){if(progressReading||!scanPending.value)return;progressReading=true;const current=generation;try{const state=await api().LibraryState();if(current===generation&&scanPending.value){scanning.value=!!state.scanning;if(state.scanning){scannedDirectories.value=state.directories||0;foundMedia.value=state.found||0}}}finally{progressReading=false}}
const browser=ref<HTMLDialogElement|null>(null),page=ref(1);
const pageSize=24;
const pageCount=computed(()=>Math.max(1,Math.ceil(filtered.value.length/pageSize)));
const pageItems=computed(()=>filtered.value.slice((page.value-1)*pageSize,page.value*pageSize));
function expand(){emit('expand',true);browser.value?.showModal()}
function closeBrowser(){browser.value?.close();emit('expand',false)}
function selectItem(id:string){closeBrowser();emit('select',id)}
const nameOrder=new Intl.Collator(undefined,{numeric:true,sensitivity:'base'});
const available=computed(()=>items.value.filter(item=>props.audioAllowed||item.kind!=='audio').sort((a,b)=>nameOrder.compare(a.name,b.name)||nameOrder.compare(a.folder,b.folder)));
const filtered=computed(()=>{const q=query.value.trim().toLocaleLowerCase();return q?available.value.filter(item=>(item.name+' '+item.folder).toLocaleLowerCase().includes(q)):available.value});
watch(available,entries=>emit('queue',entries.map(entry=>entry.id)),{immediate:true});
watch([query,available],()=>{limit.value=100;page.value=1});
function apply(state:State){directory.value=state.directory;items.value=state.items||[];limit.value=100;message.value=state.skipped?`${state.skipped} 个位置无法访问`:''}
async function scan(choose:boolean){if(scanPending.value)return;scanPending.value=true;scanning.value=false;scannedDirectories.value=0;foundMedia.value=0;message.value='';progressTimer=window.setInterval(()=>readProgress().catch(()=>{}),500);const current=++generation;try{const state=await api().ScanLibrary(choose);if(state&&current===generation)apply(state)}catch(error){if(current===generation)message.value=String(error)}finally{if(current===generation){scanning.value=false;scanPending.value=false;window.clearInterval(progressTimer)}}}
async function cancel(){window.clearInterval(progressTimer);generation++;scanning.value=false;scanPending.value=false;await api().CancelLibraryScan().catch(()=>{})}
onMounted(()=>api().LibraryState().then(apply).catch(()=>{}));
onBeforeUnmount(()=>{window.clearInterval(progressTimer);generation++;api().CancelLibraryScan().catch(()=>{})});
</script>
<template>
 <section class="side-section video-library">
  <div class="section-heading"><h2>媒体库 <span class="library-count">{{available.length}}</span></h2><div class="library-actions"><button @click="expand" title="展开媒体库" aria-label="展开媒体库">⤢</button><button v-if="directory" :disabled="scanPending" @click="scan(false)" title="重新扫描" aria-label="重新扫描">↻</button><button :disabled="scanPending" @click="scan(true)" title="选择目录" aria-label="选择目录"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"><path d="M3 7a2 2 0 0 1 2-2h5l2 2h7a2 2 0 0 1 2 2v10H3Z"/></svg></button></div></div>
  <button v-if="!directory&&!scanning" class="library-add" @click="scan(true)"><span>＋</span>选择媒体目录</button>
  <template v-if="directory"><HoverText class="library-folder" :text="directory"/><div class="library-search"><span>⌕</span><input v-model="query" placeholder="搜索媒体" aria-label="搜索媒体"></div></template>
  <div v-if="scanning" class="library-progress"><span class="scan-spinner"></span><span>扫描中 · {{scannedDirectories}} 目录 · {{foundMedia}} 媒体</span><button @click="cancel">取消</button></div>
  <div v-else-if="directory" class="library-list">
   <button v-for="item in filtered.slice(0,limit)" :key="item.id" class="library-item" :class="{selected:item.id===props.selected}" :disabled="props.busy" @click="emit('select',item.id)"><span class="library-play">{{item.id===props.selected?'▶':item.kind==='audio'?'♪':'▷'}}</span><span class="library-item-text"><HoverText class="library-title" :text="item.name"/><span v-if="item.folder&&item.folder!==item.name.replace(/\.[^.]+$/,'')" class="library-item-folder" :title="item.folder">{{item.folder}}</span></span><span v-if="item.has_script" class="script-badge" title="同名脚本">∿</span></button>
   <button v-if="filtered.length>limit" class="library-more" @click="limit+=100">显示更多 · {{filtered.length-limit}}</button>
   <div v-if="!filtered.length" class="library-empty">{{query?'没有匹配的媒体':items.length?'没有匹配的媒体':'暂无媒体'}}<button v-if="!query" @click="scan(false)">扫描目录</button></div>
  </div>
  <div v-if="message" class="library-message" role="status">{{message}}</div>
 </section>
 <Teleport to="body"><dialog ref="browser" class="library-browser" @cancel.prevent="closeBrowser" @click="($event.target===browser)&&closeBrowser()">
  <div class="browser-shell">
   <header class="browser-heading"><div><h2>媒体库 <span>{{filtered.length}}</span></h2><HoverText class="browser-directory" :text="directory"/></div><button @click="closeBrowser" aria-label="关闭媒体库">✕</button></header>
   <div class="library-search"><span>⌕</span><input v-model="query" placeholder="搜索名称或目录" aria-label="搜索名称或目录"></div>
   <div class="browser-grid"><button v-for="item in pageItems" :key="item.id" class="library-item browser-item" :class="{selected:item.id===props.selected}" :disabled="props.busy" @click="selectItem(item.id)"><span class="library-play">{{item.kind==='audio'?'♪':'▷'}}</span><span class="library-item-text"><HoverText class="library-title" :text="item.name"/><span v-if="item.folder" class="library-item-folder" :title="item.folder">{{item.folder}}</span></span><span v-if="item.has_script" class="script-badge" title="同名脚本">∿</span></button><div v-if="!filtered.length" class="library-empty">暂无匹配媒体</div></div>
   <footer class="browser-pages"><button :disabled="page<=1" @click="page=1" aria-label="第一页">«</button><button :disabled="page<=1" @click="page--">上一页</button><label><input type="number" min="1" :max="pageCount" :value="page" @change="page=Math.max(1,Math.min(pageCount,Number(($event.target as HTMLInputElement).value)||1))" aria-label="页码"> / {{pageCount}}</label><button :disabled="page>=pageCount" @click="page++">下一页</button><button :disabled="page>=pageCount" @click="page=pageCount" aria-label="最后一页">»</button></footer>
  </div>
 </dialog></Teleport>
</template>
<style scoped>
.video-library{gap:10px}.library-count{font-size:11px;color:#7f8da4;font-weight:400;margin-left:6px}.library-actions{display:flex;gap:4px}.library-actions button{width:29px;height:29px;padding:4px;display:grid;place-items:center;border-color:transparent;background:transparent;color:#a6b3cb}.library-actions button:hover{background:#252e40}.library-actions svg{width:19px;height:19px}.library-add{display:flex;align-items:center;justify-content:center;gap:9px;background:#1b2334;border:1px dashed #3c4965;padding:17px 8px;color:#bdc9e6;font-size:13px}.library-add span{font-size:20px;color:#a0adff}.library-folder{font-size:12px;color:#a6b4cc;overflow:hidden;white-space:nowrap;text-overflow:ellipsis}.library-search{display:flex;align-items:center;gap:7px;background:#101621;border:1px solid #2c3548;border-radius:8px;padding:0 9px}.library-search>span{font-size:21px;color:#7f8ea9}.library-search input{border:0;background:transparent;padding:9px 0;width:100%;font-size:12px}.library-list{max-height:248px;overflow:auto;display:flex;flex-direction:column;gap:3px;scrollbar-width:thin;scrollbar-color:#35415a transparent;padding-right:3px}.library-item{display:flex;align-items:center;gap:9px;width:100%;padding:10px 8px;min-height:42px;text-align:left;border-color:transparent;background:transparent;border-radius:8px}.library-item:hover{background:#20293b}.library-item.selected{background:#252e4a;border-color:#465482}.library-play{font-size:17px;color:#8495b9;flex-shrink:0}.selected .library-play{color:#b4bdff}.library-item-text{min-width:0;flex:1;display:flex;flex-direction:column;gap:4px}.library-title{display:block;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;font-size:12px;color:#d0d9ea}.library-item-folder{font-size:10px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;color:#7786a0}.script-badge{width:22px;height:22px;border-radius:6px;background:#203c37;color:#82d7c7;display:grid;place-items:center;font-size:18px;flex-shrink:0}.library-more{padding:8px;border:0;background:#1a2231;font-size:12px;color:#9baed0}.library-empty{display:flex;flex-direction:column;align-items:center;gap:10px;padding:20px 0;color:#7e8da6;font-size:12px}.library-empty button{font-size:12px;padding:7px 14px}.library-progress{display:flex;gap:9px;align-items:center;min-height:45px;font-size:12px;color:#aebde0}.library-progress button{margin-left:auto;padding:5px 9px;font-size:11px}.scan-spinner{width:14px;height:14px;border:2px solid #34415d;border-top-color:#a4b2ff;border-radius:50%;animation:spin 1s linear infinite}.library-message{font-size:12px;color:#e4b184;overflow-wrap:anywhere}@keyframes spin{to{transform:rotate(360deg)}}@media(min-width:1600px){.library-list{max-height:320px}}

.video-library .library-list{max-height:clamp(240px,44dvh,640px);gap:1px}.video-library .library-item{padding:7px 6px;min-height:34px}.video-library .library-item-folder{display:none}.video-library .library-title{font-size:12px}.video-library .script-badge{width:18px;height:18px;font-size:15px}
.library-browser{width:min(1180px,calc(100vw - 64px));height:min(860px,calc(100dvh - 72px));max-width:none;max-height:none;border:1px solid #34415a;border-radius:16px;padding:0;background:#131822;color:#dce2f0;box-shadow:0 24px 100px #0009}.library-browser::backdrop{background:#050810b8}.browser-shell{height:100%;display:flex;flex-direction:column;gap:16px;padding:22px}.browser-heading{display:flex;justify-content:space-between;gap:16px;align-items:center}.browser-heading h2{margin:0;font-size:18px}.browser-heading h2 span{font-size:12px;font-weight:400;color:#8a99b2;margin-left:8px}.browser-heading>div{min-width:0}.browser-directory{font-size:12px;color:#8796af;margin-top:8px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.browser-grid{flex:1;min-height:0;overflow:auto;display:grid;grid-template-columns:repeat(2,minmax(0,1fr));grid-auto-rows:minmax(62px,auto);align-content:start;gap:6px;scrollbar-width:thin;scrollbar-color:#35415a transparent}.browser-item{padding:12px;background:#1a2231;border:1px solid #293346;min-height:62px}.browser-item .library-title{white-space:normal;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;line-height:1.5}.browser-pages{display:flex;justify-content:center;align-items:center;gap:8px;border-top:1px solid #293346;padding-top:16px}.browser-pages button{font-size:12px;padding:8px 12px}.browser-pages label{font-size:12px;color:#9cacc7;white-space:nowrap}.browser-pages input{width:58px;padding:7px;text-align:center}@media(max-width:780px){.browser-grid{grid-template-columns:1fr}.library-browser{width:calc(100vw - 24px);height:calc(100dvh - 32px)}.browser-shell{padding:14px}}
.video-library .library-list{flex:1;min-height:0;max-height:none;padding-right:4px;gap:5px}
.video-library .library-item{flex:0 0 auto;height:auto;align-items:center;padding:10px 8px;gap:9px;border-radius:8px}
.video-library .library-title{white-space:nowrap;overflow:hidden;line-height:1.65;font-size:12px}
.video-library .library-play{padding-top:0;font-size:16px;line-height:1;display:flex;align-items:center}
.video-library .script-badge{margin-top:0}
.video-library .library-item.selected{background:#203d38;border-color:#3c6055}
.video-library .library-item:hover{background:#1d2c38}
.video-library .library-search{background:#0e161f;border-color:#2b3a48}
.video-library .library-folder{padding:2px 0 4px}
.browser-item .library-title{display:block;white-space:nowrap;overflow:hidden;-webkit-line-clamp:unset}
</style>
