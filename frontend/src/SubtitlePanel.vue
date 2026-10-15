<script setup lang="ts">
import {ref,computed,watch,onMounted,onBeforeUnmount,nextTick} from 'vue';
const props=defineProps<{time:number;source:string;audioOnly:boolean}>();
const emit=defineEmits<{caption:[text:{original:string;translation:string}];picker:[open:boolean]}>();
type Cue={start_ms:number;end_ms:number;original:string;translation:string};
type State={running:boolean;message:string;processed_ms:number;cues:Cue[]};
type Provider={id:string;name:string;base_url:string};
type Profile={provider:string;base_url:string;model:string;json_mode:boolean;has_key:boolean;model_selected:boolean};
type Settings={selected:string;providers:Provider[];profiles:Profile[]};
type API={FetchAIModels:(provider:string)=>Promise<string[]>;AISettings:()=>Promise<Settings>;SaveAIProfile:(profile:Profile,key:string)=>Promise<Settings>;DeleteAIKey:(provider:string)=>Promise<Settings>;PlaybackSubtitle:(text:string)=>Promise<void>;StartSubtitles:(key:string,language:string,time:number,lowSpeech:boolean)=>Promise<void>;StopSubtitles:()=>Promise<void>;SubtitleState:(time:number)=>Promise<State>};
const api=()=>(window as unknown as {go:{main:{App:API}}}).go.main.App;
const savedKey=ref(false),lowSpeech=ref(true),configReady=ref(false);
const providers=ref<Provider[]>([]),profiles=ref<Profile[]>([]),provider=ref('deepseek'),baseURL=ref(''),model=ref('');
const chosenProvider=computed(()=>providers.value.find(p=>p.id===provider.value));
function chooseProvider(){const p=profiles.value.find(p=>p.provider===provider.value);baseURL.value=p?.base_url||chosenProvider.value?.base_url||'';model.value=p?.model_selected?p.model:'';savedKey.value=!!p?.has_key;key.value='';message.value=''}
function applySettings(settings:Settings){providers.value=settings.providers;profiles.value=settings.profiles;provider.value=settings.selected;chooseProvider();configReady.value=true}
function draft():Profile{return {provider:provider.value,base_url:baseURL.value,model:model.value,json_mode:false,has_key:false,model_selected:!!model.value}}
async function persist(){const settings=await api().SaveAIProfile(draft(),key.value.trim());applySettings(settings)}
const enabled=ref(false),bilingual=ref(false),key=ref(''),language=ref(''),busy=ref(false),message=ref(''),processed=ref(0);
const modelDialog=ref<HTMLDialogElement|null>(null),modelLoading=ref(false),models=ref<string[]>([]),modelQuery=ref(''),modelError=ref(''),modelLimit=ref(100);let modelRequest=0;
const filteredModels=computed(()=>models.value.filter(id=>id.toLocaleLowerCase().includes(modelQuery.value.trim().toLocaleLowerCase())));
watch(modelQuery,()=>modelLimit.value=100);
function closeModels(){modelRequest++;modelLoading.value=false;modelDialog.value?.close();emit('picker',false)}
async function fetchModels(){
 if(enabled.value||modelLoading.value||busy.value||!configReady.value)return;
 if(!key.value.trim()&&!savedKey.value){model.value='';message.value='填写 API Key 后获取模型';return}
 if(!baseURL.value.trim()){model.value='';message.value='请填写 API 地址';return}
 const request=++modelRequest;modelLoading.value=true;models.value=[];modelQuery.value='';modelError.value='';model.value='';emit('picker',true);await nextTick();modelDialog.value?.showModal();
 try{await persist();if(request!==modelRequest)return;const list=await api().FetchAIModels(provider.value);if(request!==modelRequest)return;models.value=list;message.value='请选择模型'}catch(error){if(request===modelRequest){modelError.value=String(error);message.value=String(error)}}finally{if(request===modelRequest)modelLoading.value=false}
}
async function providerChanged(){chooseProvider();model.value='';await fetchModels()}
async function selectModel(id:string){if(busy.value||modelLoading.value)return;busy.value=true;const request=modelRequest;try{model.value=id;await persist();if(request!==modelRequest)return;closeModels();message.value='模型已选择'}catch(error){model.value='';modelError.value=String(error)}finally{busy.value=false}}
let timer:number,reading=false,generation=0,signature='',cues:Cue[]=[];
function updateCaption(){const active=enabled.value?cues.filter(c=>c.start_ms<=props.time&&props.time<c.end_ms):[];emit('caption',{original:bilingual.value?active.map(c=>c.original).join('\n'):'',translation:active.map(c=>c.translation).join('\n')})}
function clearTrack(){signature='';emit('caption',{original:'',translation:''});api().PlaybackSubtitle('').catch(()=>{})}
function srtTime(ms:number){const n=Math.max(0,Math.round(ms));return String(Math.floor(n/3600000)).padStart(2,'0')+':'+String(Math.floor(n/60000)%60).padStart(2,'0')+':'+String(Math.floor(n/1000)%60).padStart(2,'0')+','+String(n%1000).padStart(3,'0')}
function refreshTrack(force=false){updateCaption();if(props.audioOnly)return;const last=cues.at(-1);const next=String(cues.length)+'|'+last?.end_ms+'|'+last?.translation+'|'+bilingual.value+'|'+enabled.value;if(!force&&signature===next)return;signature=next;const text=enabled.value?cues.map((c,i)=>String(i+1)+'\n'+srtTime(c.start_ms)+' --> '+srtTime(c.end_ms)+'\n'+(bilingual.value?c.original+'\n':'')+c.translation+'\n').join('\n'):'';api().PlaybackSubtitle(text).catch(error=>message.value=String(error))}
async function changeEnabled(){
  const turn=++generation;busy.value=true;
  try{
    if(enabled.value){if(!key.value.trim()&&!savedKey.value)throw new Error('请填写当前服务商的 API Key');await persist();await api().StartSubtitles('',language.value,Math.max(0,props.time),lowSpeech.value);if(turn!==generation)return;savedKey.value=true;key.value='';message.value='正在加载语音模型';await readState();}
    else{await api().StopSubtitles();message.value='已停止';}
  }catch(error){if(turn===generation){enabled.value=false;message.value=String(error);}}
  finally{if(turn===generation){busy.value=false;refreshTrack();}}
}
async function saveKey(){await fetchModels()}
async function deleteKey(){busy.value=true;try{const selected=provider.value;const settings=await api().DeleteAIKey(selected);profiles.value=settings.profiles;chooseProvider();message.value='Key 已删除'}catch(error){message.value=String(error)}finally{busy.value=false}}
async function readState(){
  if(!enabled.value||reading)return;reading=true;const turn=generation;
  try{const state=await api().SubtitleState(Math.max(0,props.time));if(turn!==generation)return;cues=state.cues||[];processed.value=state.processed_ms;message.value=state.message;refreshTrack();}
  catch(error){if(turn===generation)message.value=String(error);}
  finally{reading=false;}
}
function formatTime(ms:number){const seconds=Math.floor(ms/1000);return Math.floor(seconds/60)+':'+String(seconds%60).padStart(2,'0');}
watch(bilingual,()=>refreshTrack(true));
watch(()=>props.time,updateCaption);
watch(enabled,updateCaption);

watch(()=>props.source,()=>{generation++;enabled.value=false;busy.value=false;cues=[];processed.value=0;message.value='';clearTrack();api().StopSubtitles().catch(()=>{});});
onMounted(()=>{api().AISettings().then(async settings=>{applySettings(settings);if(!model.value&&savedKey.value)await fetchModels()}).catch(error=>message.value=String(error));timer=window.setInterval(readState,500);});
onBeforeUnmount(()=>{closeModels();generation++;window.clearInterval(timer);clearTrack();key.value='';api().StopSubtitles().catch(()=>{});});
</script>
<template>
  <section class="side-section">
    <div class="section-heading"><h2 class="subtitle-heading">字幕翻译<button class="current-model" :disabled="enabled||busy||modelLoading||!configReady" :title="model||'选择模型'" @click="fetchModels">{{model||'选择模型'}}</button></h2><span v-if="processed" class="auto-tag">{{formatTime(processed)}}</span></div>
    <label class="field">AI 服务<select v-model="provider" :disabled="enabled||busy||modelLoading||!configReady" @change="providerChanged"><option v-for="p in providers" :key="p.id" :value="p.id">{{p.name}}</option></select></label>

    <label v-if="provider==='custom'" class="field">API 地址<input v-model="baseURL" :disabled="enabled||busy||modelLoading||!configReady" @change="model='';fetchModels()" placeholder="https://example.com/v1" spellcheck="false"></label>
    <label class="field">API Key<input v-model="key" type="password" :placeholder="savedKey?'已保存，留空使用现有 Key':'填写 API Key'" autocomplete="off" spellcheck="false" @change="fetchModels" :disabled="enabled||busy||modelLoading"></label>
    <div class="key-actions"><button :disabled="enabled||busy||!configReady||!baseURL.trim()||modelLoading||(!key.trim()&&!savedKey)" @click="saveKey">{{modelLoading?'获取中…':'获取模型'}}</button><button v-if="savedKey" :disabled="enabled||busy" @click="deleteKey">删除密钥</button></div>
    <label class="field">原文语言<select v-model="language" :disabled="enabled||busy"><option value="">自动识别</option><option value="ja">日语</option><option value="en">英语</option><option value="zh">中文</option></select></label>
    <div class="channel-switches"><label>低语增强<input type="checkbox" role="switch" v-model="lowSpeech" :disabled="enabled||busy"></label><label>中文字幕<input type="checkbox" role="switch" v-model="enabled" :disabled="!source||busy||modelLoading||!configReady||!model" @change="changeEnabled"></label><label>双语字幕<input type="checkbox" role="switch" v-model="bilingual"></label></div>
    <div v-if="message" class="subtitle-status" role="status">{{message}}</div>
  </section>
  <Teleport to="body"><dialog ref="modelDialog" class="model-dialog" @cancel.prevent="closeModels">
   <header><h2>选择模型</h2><button :disabled="busy" @click="closeModels" aria-label="关闭">✕</button></header>
   <input v-model="modelQuery" class="model-search" placeholder="搜索模型" aria-label="搜索模型" :disabled="modelLoading">
   <div v-if="modelLoading" class="model-empty">正在获取模型…</div>
   <div v-else-if="modelError" class="model-empty model-error">{{modelError}}<button @click="fetchModels">重新获取</button></div>
   <div v-else class="model-results"><button v-for="id in filteredModels.slice(0,modelLimit)" :key="id" :disabled="busy" @click="selectModel(id)" :title="id">{{id}}<span>›</span></button><button v-if="filteredModels.length>modelLimit" @click="modelLimit+=100">显示更多</button><div v-if="!filteredModels.length" class="model-empty">没有匹配的模型</div></div>
  </dialog></Teleport>
</template>
<style>
.subtitle-heading{display:flex;align-items:baseline;gap:8px;min-width:0}.current-model{font-size:10px!important;color:#8fa1bc;background:transparent;border:0;border-radius:3px;padding:0!important;max-width:130px;overflow:hidden;white-space:nowrap;text-overflow:ellipsis;font-weight:400}.current-model:hover{color:#becaff;background:transparent}.model-dialog{width:min(600px,calc(100vw - 48px));max-height:75dvh;padding:20px;background:#131b29;color:#dce5f4;border:1px solid #35415a;border-radius:15px;box-shadow:0 24px 100px #0009}.model-dialog::backdrop{background:#050810b8}.model-dialog header{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:16px}.model-dialog h2{font-size:17px;margin:0}.model-dialog header button{padding:6px 10px;background:transparent;border:0}.model-search{width:100%;margin-bottom:12px}.model-results{max-height:50dvh;overflow:auto;scrollbar-width:thin;scrollbar-color:#35415a transparent;display:flex;flex-direction:column;gap:5px}.model-results>button{display:flex;align-items:center;justify-content:space-between;gap:12px;text-align:left;font-size:13px;overflow-wrap:anywhere;padding:12px;background:#1a2435;border-color:#29364b}.model-results>button:hover{background:#263451;border-color:#6374a1}.model-results>button span{color:#8c9fc1}.model-empty{padding:28px 12px;text-align:center;color:#a3b2cb;display:flex;align-items:center;flex-direction:column;gap:16px}.model-error{color:#e9b297}
.subtitle-status{font-size:13px;line-height:1.6;color:#a9b8d1;overflow-wrap:anywhere}.video-stage video::cue{color:#fff;background:rgba(0,0,0,.72);font-size:20px;font-family:Segoe UI,Microsoft YaHei,sans-serif}</style>

<style>.key-actions{display:flex;gap:8px}.key-actions button{flex:1;font-size:12px;padding:8px}</style>
