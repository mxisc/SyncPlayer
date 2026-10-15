<script setup lang="ts">
import {ref,nextTick,onBeforeUnmount,watch} from 'vue';
const props=defineProps<{text:string}>();
const root=ref<HTMLElement>(),track=ref<HTMLElement>(),moving=ref(false);
let animation:Animation|undefined,hovered=false,generation=0;
function stop(){hovered=false;generation++;animation?.cancel();animation=undefined;moving.value=false}
async function start(){
 stop();hovered=true;const current=generation;
 if(!root.value||!track.value)return;
 const distance=track.value.scrollWidth-root.value.clientWidth;
 if(distance<=1)return;
 moving.value=true;await nextTick();
 if(!hovered||current!==generation||!track.value)return;
 animation=track.value.animate([
  {transform:'translateX(0)',offset:0},
  {transform:'translateX(0)',offset:.15},
  {transform:`translateX(-${distance}px)`,offset:.85},
  {transform:`translateX(-${distance}px)`,offset:1},
 ],{duration:Math.max(2600,distance/35*1000+1400),iterations:Infinity,direction:'alternate',easing:'linear'});
}
watch(()=>props.text,stop);onBeforeUnmount(stop);
</script>
<template><span ref="root" class="hover-text" :class="{moving}" :title="text" @mouseenter="start" @mouseleave="stop"><span ref="track" class="hover-text-track">{{text}}</span></span></template>
<style scoped>
.hover-text{display:block;min-width:0;width:100%;overflow:hidden;white-space:nowrap;line-height:1.65;text-align:left}
.hover-text-track{display:block;width:100%;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.hover-text.moving .hover-text-track{width:max-content;overflow:visible;text-overflow:clip;will-change:transform}
</style>
