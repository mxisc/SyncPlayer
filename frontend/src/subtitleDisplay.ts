type SubtitleCue={start_ms:number;end_ms:number;original:string;translation:string};

const withoutAnnotations=(text:string)=>text.replace(/\[[^\[\]\r\n]*\]|［[^［］\r\n]*］/gu,' ').trim();

export function displaySubtitleCues(cues:SubtitleCue[]):SubtitleCue[]{
 return cues.flatMap(cue=>{
  const original=withoutAnnotations(cue.original);
  // Only explicit line breaks supplied by the translator create display cues.
  const sentences=withoutAnnotations(cue.translation)
   .split(/\r?\n/u)
   .map(text=>text.replace(/(?<=\p{Script=Han})\s*[，、；：…,;:]+\s*(?=\p{Script=Han})/gu,'')
    .replace(/[，。！？、；：…,!?;:]+|(?<!\d)\.|\.(?!\d)/gu,' ').replace(/\s+/gu,' ').trim())
   .filter(Boolean);
  if(!sentences.length)return [];
  const duration=cue.end_ms-cue.start_ms;
  if(duration<sentences.length)return [{...cue,original,translation:sentences.join(' ')}];
  const weights=sentences.map(text=>Math.max(1,Array.from(text).length));
  const total=weights.reduce((a,b)=>a+b,0);
  let elapsed=0,start=cue.start_ms;
  return sentences.map((translation,i)=>{
   elapsed+=weights[i];
   const end=i===sentences.length-1?cue.end_ms:Math.min(cue.end_ms-(sentences.length-1-i),Math.max(start+1,cue.start_ms+Math.round(duration*elapsed/total)));
   const result={...cue,original,start_ms:start,end_ms:end,translation};
   start=end;
   return result;
  });
 });
}
