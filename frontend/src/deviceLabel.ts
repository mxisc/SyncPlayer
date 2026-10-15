type DeviceIdentity={name:string;identity:string;address?:string};

export function deviceLabel(device:DeviceIdentity):string {
 const handy=/^OHD_hw(\d+)_(.+)$/i.exec(device.name);
 const model=handy?.[1]==='4'?'The Handy 2 Pro':handy?.[1]==='3'?'The Handy 2':handy||/^the handy$/i.test(device.name)?'The Handy':device.name;
 let id=handy?.[2]||device.address?.replace(/[^a-f\d]/gi,'');
 if(!id){
  let hash=2166136261;
  for(const char of device.identity)hash=Math.imul(hash^char.charCodeAt(0),16777619);
  id=(hash>>>0).toString(16).padStart(8,'0');
 }
 return `${model} · ${id.slice(-6).toUpperCase()}`;
}
