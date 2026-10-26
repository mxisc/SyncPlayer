import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import ts from 'typescript';
const source=readFileSync(new URL('./playlist.ts',import.meta.url),'utf8');
const compiled=ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.ESNext}}).outputText;
const {nextMedia}=await import('data:text/javascript;base64,'+Buffer.from(compiled).toString('base64'));
const ids=['a','b','c'];
test('sequential playback advances and stops at the end',()=>{
 assert.equal(nextMedia(ids,'a','sequence'),'b');
 assert.equal(nextMedia(ids,'c','sequence'),null);
});
test('list and single repeats preserve their boundaries',()=>{
 assert.equal(nextMedia(ids,'c','repeat-all'),'a');
 assert.equal(nextMedia(ids,'b','repeat-one'),'b');
 assert.equal(nextMedia([], 'external','repeat-one'),'external');
 assert.equal(nextMedia(ids,'b','once'),null);
});
test('shuffle excludes the current item when alternatives exist',()=>{
 assert.equal(nextMedia(ids,'b','shuffle',()=>0),'a');
 assert.equal(nextMedia(ids,'b','shuffle',()=>0.99),'c');
 assert.equal(nextMedia(['a'],'a','shuffle'),'a');
});
test('missing items and empty queues do not start an unrelated item',()=>{
 for(const mode of ['sequence','repeat-all','shuffle']){
  assert.equal(nextMedia(ids,'removed',mode),null);
  assert.equal(nextMedia([],'a',mode),null);
 }
});
