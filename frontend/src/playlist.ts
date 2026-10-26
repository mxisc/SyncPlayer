export type PlaybackOrder='once'|'sequence'|'repeat-all'|'repeat-one'|'shuffle';
export function nextMedia(ids:string[],current:string,mode:PlaybackOrder,random=Math.random):string|null {
 if(mode==='once')return null;
 if(mode==='repeat-one')return current||null;
 const index=ids.indexOf(current);
 if(index<0)return null;
 if(mode==='shuffle'){
  const candidates=ids.filter(id=>id!==current);
  return candidates.length?candidates[Math.min(candidates.length-1,Math.floor(random()*candidates.length))]:current;
 }
 if(index+1<ids.length)return ids[index+1];
 return mode==='repeat-all'?ids[0]||null:null;
}
