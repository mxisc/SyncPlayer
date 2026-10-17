<script setup lang="ts">
import {ref,onMounted,onBeforeUnmount} from 'vue';
const emit=defineEmits<{close:[];saved:[]}>();
const api=()=> (window as any).go.main.App;
const domain=ref(''),id=ref(''),token=ref(''),localIP=ref(''),message=ref(''),busy=ref(false),agree=ref(false);
const hasToken=ref(false),savedDomain=ref(''),showToken=ref(false),revealedToken=ref(''),revealing=ref(false);let alive=true;
async function toggleToken(){if(showToken.value){showToken.value=false;revealedToken.value='';return}if(token.value){showToken.value=true;return}if(!hasToken.value){showToken.value=true;return}revealing.value=true;try{const value=await api().RevealDeoHTTPSToken();if(alive){revealedToken.value=value;showToken.value=true}}catch{if(alive)message.value='无法读取已保存的 Token'}finally{revealing.value=false}}
function editToken(event:Event){token.value=(event.target as HTMLInputElement).value;revealedToken.value=''}
const dialog=ref<HTMLElement>();
function trapTab(event:KeyboardEvent){if(event.key!=='Tab')return;const fields=Array.from(dialog.value?.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),a[href]')||[]);if(!fields.length){event.preventDefault();dialog.value?.focus();return}const first=fields[0],last=fields[fields.length-1];if(event.shiftKey&&(document.activeElement===first||document.activeElement===dialog.value)){event.preventDefault();last.focus()}else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus()}}
onMounted(()=>dialog.value?.focus());
onMounted(async()=>{try{const s=await api().DeoHTTPSStatus();if(!alive)return;domain.value=s.domain;savedDomain.value=s.domain;id.value=s.id;hasToken.value=s.has_token;localIP.value=s.local_ip;message.value=s.message||(s.ready?'已配置，证书有效':'')}catch{message.value='无法读取 HTTPS 配置'}});
onBeforeUnmount(()=>{alive=false;token.value='';revealedToken.value='';id.value=''});
async function save(){if(busy.value)return;busy.value=true;message.value='正在保存并申请证书，DNS 验证可能需要数分钟，请保持窗口打开…';try{await api().ConfigureDeoHTTPS(domain.value,id.value,token.value,agree.value);token.value='';id.value='';emit('saved')}catch(e){message.value=String(e)}finally{busy.value=false}}
</script>
<template>
<div class="https-backdrop" @keydown.stop="trapTab" @keydown.esc.prevent="!busy&&emit('close')">
 <section ref="dialog" class="https-dialog" role="dialog" aria-modal="true" aria-labelledby="https-title" tabindex="-1">
  <div class="section-heading"><h2 id="https-title">配置 DeoVR 远程 HTTPS</h2><button :disabled="busy" aria-label="关闭配置" @click="emit('close')">×</button></div>
  <p><strong>仅支持 DeoVR Classic；新版 DeoVR（原 Hyper）暂不支持。</strong></p><p>首次使用需要准备自己的域名和 DNSPod Token。配置只在本机加密保存，申请及续期时会连接 DNSPod 和证书服务。</p>
  <ol>
   <li>准备一个由 DNSPod 托管解析的域名，为本机选择一个专用子域名，例如 <code>vr.example.com</code>。</li>
   <li>在 DNSPod 为该子域名添加 A 记录，指向这台电脑的局域网 IPv4：<strong>{{localIP||'请在 Windows 网络设置中查看'}}</strong>。建议在路由器中固定电脑 IP；IP 变化后自行更新解析。不要填写公网 IP，也不需要公网端口转发。</li>
   <li>在 DNSPod「账号中心 → 密钥管理 → DNSPod Token」创建 Token，将 ID 和 Token 分别填在下面。这里使用传统 DNSPod Token，不能填写腾讯云 SecretId / SecretKey。<a href="https://docs.dnspod.cn/account/dnspod-token/" target="_blank" rel="noreferrer">查看官方步骤</a></li>
   <li>电脑和头显连接同一局域网，允许 SyncPlayer 通过 Windows 防火墙的专用网络。HTTPS 使用 TCP 443 端口，请确保未被其他程序占用。</li>
  </ol>
  <form @submit.prevent="save">
   <label class="field">域名<input v-model.trim="domain" required autofocus placeholder="vr.example.com（不含 https:// 和路径）" :disabled="busy" autocomplete="off"></label>
   <div class="https-fields"><label class="field">DNSPod ID<input v-model.trim="id" required inputmode="numeric" autocomplete="off" :disabled="busy"></label><div class="field"><label for="https-token">DNSPod Token</label><div class="https-token-row"><input id="https-token" :value="token||revealedToken" @input="editToken" :required="!hasToken||domain.trim().toLowerCase()!==savedDomain" :type="showToken?'text':'password'" :placeholder="hasToken?'••••••••••••••••':'填写 DNSPod Token'" autocomplete="new-password" :disabled="busy||revealing"><button type="button" class="https-eye" :disabled="busy||revealing" :aria-label="showToken?'隐藏 Token':'显示 Token'" :title="showToken?'隐藏 Token':'显示 Token'" :aria-pressed="showToken" @click="toggleToken"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/><path v-if="showToken" d="m3 3 18 18"/></svg></button></div></div></div>
   <label class="https-agree"><input type="checkbox" v-model="agree" :disabled="busy" required>我已阅读并同意 <a href="https://letsencrypt.org/repository/" target="_blank" rel="noreferrer">Let's Encrypt 证书服务条款</a></label>
   <p class="https-note">证书申请成功仅代表证书就绪。若头显仍无法打开地址，请检查域名解析、防火墙，以及路由器是否拦截指向内网 IP 的域名。程序运行并启用远程媒体服务时会自动检查续期；域名和凭据由你维护。</p>
   <p v-if="message" class="https-message" role="status">{{message}}</p>
   <div class="https-actions"><button type="button" :disabled="busy" @click="emit('close')">取消</button><button class="primary" :disabled="busy||!agree" type="submit">{{busy?'正在申请…':'保存并申请证书'}}</button></div>
  </form>
 </section>
</div>
</template>
<style>
.https-backdrop{position:fixed;inset:0;z-index:10000;background:#050810c9;display:flex;align-items:center;justify-content:center;padding:24px}.https-dialog{width:min(680px,100%);max-height:calc(100dvh - 48px);overflow:auto;background:#151c28;color:#e2e8f2;border:1px solid #364154;border-radius:16px;padding:24px;box-shadow:0 24px 80px #0009;font-size:14px;line-height:1.65}.https-dialog h2{font-size:19px;margin:0}.https-dialog p,.https-dialog li{color:#b8c4d5}.https-dialog ol{padding-left:22px}.https-dialog li+li{margin-top:10px}.https-dialog a{color:#9cdbd2}.https-dialog code{overflow-wrap:anywhere}.https-fields{display:grid;grid-template-columns:1fr 2fr;gap:12px}.https-dialog .field{margin:12px 0}.https-dialog input:not([type=checkbox]){width:100%;box-sizing:border-box}.https-agree{display:flex;align-items:center;flex-wrap:wrap;gap:6px}.https-agree input{width:auto}.https-note{font-size:12px}.https-message{padding:10px 12px;background:#202b3b;border-radius:8px;overflow-wrap:anywhere}.https-actions{display:flex;justify-content:flex-end;gap:10px}.https-dialog button:disabled{opacity:.5}
.https-token-row{display:flex;align-items:center;gap:6px}.https-token-row input{min-width:0;flex:1}.https-eye{display:flex;align-items:center;justify-content:center;padding:9px;flex-shrink:0}.https-eye svg{width:19px;height:19px}
</style>
