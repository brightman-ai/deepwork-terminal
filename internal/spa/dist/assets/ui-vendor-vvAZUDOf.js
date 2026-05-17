import{h as gt,r as ot,w as pt,s as J,c as X,a as Jt,g as Qt,o as te,u as ee}from"./vue-vendor-CbLGfPc7.js";/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const ne=t=>{for(const e in t)if(e.startsWith("aria-")||e==="role"||e==="title")return!0;return!1};/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Lt=t=>t==="";/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const oe=(...t)=>t.filter((e,n,o)=>!!e&&e.trim()!==""&&o.indexOf(e)===n).join(" ").trim();/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Pt=t=>t.replace(/([a-z0-9])([A-Z])/g,"$1-$2").toLowerCase();/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const ie=t=>t.replace(/^([A-Z])|[\s-_]+(\w)/g,(e,n,o)=>o?o.toUpperCase():n.toLowerCase());/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const re=t=>{const e=ie(t);return e.charAt(0).toUpperCase()+e.slice(1)};/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */var it={xmlns:"http://www.w3.org/2000/svg",width:24,height:24,viewBox:"0 0 24 24",fill:"none",stroke:"currentColor","stroke-width":2,"stroke-linecap":"round","stroke-linejoin":"round"};/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const se=({name:t,iconNode:e,absoluteStrokeWidth:n,"absolute-stroke-width":o,strokeWidth:i,"stroke-width":s,size:r=it.width,color:c=it.stroke,...f},{slots:l})=>gt("svg",{...it,...f,width:r,height:r,stroke:c,"stroke-width":Lt(n)||Lt(o)||n===!0||o===!0?Number(i||s||it["stroke-width"])*24/Number(r):i||s||it["stroke-width"],class:oe("lucide",f.class,...t?[`lucide-${Pt(re(t))}-icon`,`lucide-${Pt(t)}`]:["lucide-icon"]),...!l.default&&!ne(f)&&{"aria-hidden":"true"}},[...e.map(a=>gt(...a)),...l.default?[l.default()]:[]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const k=(t,e)=>(n,{slots:o,attrs:i})=>gt(se,{...i,...n,iconNode:e,name:t},o);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const on=k("book-open",[["path",{d:"M12 7v14",key:"1akyts"}],["path",{d:"M3 18a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h5a4 4 0 0 1 4 4 4 4 0 0 1 4-4h5a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1h-6a3 3 0 0 0-3 3 3 3 0 0 0-3-3z",key:"ruj8y"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const rn=k("bot",[["path",{d:"M12 8V4H8",key:"hb8ula"}],["rect",{width:"16",height:"12",x:"4",y:"8",rx:"2",key:"enze0r"}],["path",{d:"M2 14h2",key:"vft8re"}],["path",{d:"M20 14h2",key:"4cs60a"}],["path",{d:"M15 13v2",key:"1xurst"}],["path",{d:"M9 13v2",key:"rq6x2g"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const sn=k("brain-circuit",[["path",{d:"M12 5a3 3 0 1 0-5.997.125 4 4 0 0 0-2.526 5.77 4 4 0 0 0 .556 6.588A4 4 0 1 0 12 18Z",key:"l5xja"}],["path",{d:"M9 13a4.5 4.5 0 0 0 3-4",key:"10igwf"}],["path",{d:"M6.003 5.125A3 3 0 0 0 6.401 6.5",key:"105sqy"}],["path",{d:"M3.477 10.896a4 4 0 0 1 .585-.396",key:"ql3yin"}],["path",{d:"M6 18a4 4 0 0 1-1.967-.516",key:"2e4loj"}],["path",{d:"M12 13h4",key:"1ku699"}],["path",{d:"M12 18h6a2 2 0 0 1 2 2v1",key:"105ag5"}],["path",{d:"M12 8h8",key:"1lhi5i"}],["path",{d:"M16 8V5a2 2 0 0 1 2-2",key:"u6izg6"}],["circle",{cx:"16",cy:"13",r:".5",key:"ry7gng"}],["circle",{cx:"18",cy:"3",r:".5",key:"1aiba7"}],["circle",{cx:"20",cy:"21",r:".5",key:"yhc1fs"}],["circle",{cx:"20",cy:"8",r:".5",key:"1e43v0"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const cn=k("chevron-down",[["path",{d:"m6 9 6 6 6-6",key:"qrunsl"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const ln=k("chevron-left",[["path",{d:"m15 18-6-6 6-6",key:"1wnfg3"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const an=k("chevron-up",[["path",{d:"m18 15-6-6-6 6",key:"153udz"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const fn=k("chevron-right",[["path",{d:"m9 18 6-6-6-6",key:"mthhwq"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const un=k("clipboard-copy",[["rect",{width:"8",height:"4",x:"8",y:"2",rx:"1",ry:"1",key:"tgr4d6"}],["path",{d:"M8 4H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2",key:"4jdomd"}],["path",{d:"M16 4h2a2 2 0 0 1 2 2v4",key:"3hqy98"}],["path",{d:"M21 14H11",key:"1bme5i"}],["path",{d:"m15 10-4 4 4 4",key:"5dvupr"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const dn=k("clipboard-list",[["rect",{width:"8",height:"4",x:"8",y:"2",rx:"1",ry:"1",key:"tgr4d6"}],["path",{d:"M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2",key:"116196"}],["path",{d:"M12 11h4",key:"1jrz19"}],["path",{d:"M12 16h4",key:"n85exb"}],["path",{d:"M8 11h.01",key:"1dfujw"}],["path",{d:"M8 16h.01",key:"18s6g9"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const hn=k("ellipsis",[["circle",{cx:"12",cy:"12",r:"1",key:"41hilf"}],["circle",{cx:"19",cy:"12",r:"1",key:"1wjl8i"}],["circle",{cx:"5",cy:"12",r:"1",key:"1pcz8c"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const mn=k("file-pen-line",[["path",{d:"m18.226 5.226-2.52-2.52A2.4 2.4 0 0 0 14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-.351",key:"1k2beg"}],["path",{d:"M21.378 12.626a1 1 0 0 0-3.004-3.004l-4.01 4.012a2 2 0 0 0-.506.854l-.837 2.87a.5.5 0 0 0 .62.62l2.87-.837a2 2 0 0 0 .854-.506z",key:"2t3380"}],["path",{d:"M8 18h1",key:"13wk12"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const pn=k("folder-kanban",[["path",{d:"M4 20h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.93a2 2 0 0 1-1.66-.9l-.82-1.2A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13c0 1.1.9 2 2 2Z",key:"1fr9dc"}],["path",{d:"M8 10v4",key:"tgpxqk"}],["path",{d:"M12 10v2",key:"hh53o1"}],["path",{d:"M16 10v6",key:"1d6xys"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const yn=k("globe",[["circle",{cx:"12",cy:"12",r:"10",key:"1mglay"}],["path",{d:"M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20",key:"13o1zl"}],["path",{d:"M2 12h20",key:"9i4pu4"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const gn=k("info",[["circle",{cx:"12",cy:"12",r:"10",key:"1mglay"}],["path",{d:"M12 16v-4",key:"1dtifu"}],["path",{d:"M12 8h.01",key:"e9boi3"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const wn=k("list",[["path",{d:"M3 5h.01",key:"18ugdj"}],["path",{d:"M3 12h.01",key:"nlz23k"}],["path",{d:"M3 19h.01",key:"noohij"}],["path",{d:"M8 5h13",key:"1pao27"}],["path",{d:"M8 12h13",key:"1za7za"}],["path",{d:"M8 19h13",key:"m83p4d"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const xn=k("message-square",[["path",{d:"M22 17a2 2 0 0 1-2 2H6.828a2 2 0 0 0-1.414.586l-2.202 2.202A.71.71 0 0 1 2 21.286V5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2z",key:"18887p"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const vn=k("palette",[["path",{d:"M12 22a1 1 0 0 1 0-20 10 9 0 0 1 10 9 5 5 0 0 1-5 5h-2.25a1.75 1.75 0 0 0-1.4 2.8l.3.4a1.75 1.75 0 0 1-1.4 2.8z",key:"e79jfc"}],["circle",{cx:"13.5",cy:"6.5",r:".5",fill:"currentColor",key:"1okk4w"}],["circle",{cx:"17.5",cy:"10.5",r:".5",fill:"currentColor",key:"f64h9f"}],["circle",{cx:"6.5",cy:"12.5",r:".5",fill:"currentColor",key:"qy21gx"}],["circle",{cx:"8.5",cy:"7.5",r:".5",fill:"currentColor",key:"fotxhn"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const bn=k("panel-left",[["rect",{width:"18",height:"18",x:"3",y:"3",rx:"2",key:"afitv7"}],["path",{d:"M9 3v18",key:"fh3hqa"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const kn=k("panel-right",[["rect",{width:"18",height:"18",x:"3",y:"3",rx:"2",key:"afitv7"}],["path",{d:"M15 3v18",key:"14nvp0"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const An=k("panels-top-left",[["rect",{width:"18",height:"18",x:"3",y:"3",rx:"2",key:"afitv7"}],["path",{d:"M3 9h18",key:"1pudct"}],["path",{d:"M9 21V9",key:"1oto5p"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Mn=k("pin",[["path",{d:"M12 17v5",key:"bb1du9"}],["path",{d:"M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 2 2 0 0 0 0-4H8a2 2 0 0 0 0 4 1 1 0 0 1 1 1z",key:"1nkz8b"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Cn=k("search",[["path",{d:"m21 21-4.34-4.34",key:"14j7rj"}],["circle",{cx:"11",cy:"11",r:"8",key:"4ej97u"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const On=k("settings",[["path",{d:"M9.671 4.136a2.34 2.34 0 0 1 4.659 0 2.34 2.34 0 0 0 3.319 1.915 2.34 2.34 0 0 1 2.33 4.033 2.34 2.34 0 0 0 0 3.831 2.34 2.34 0 0 1-2.33 4.033 2.34 2.34 0 0 0-3.319 1.915 2.34 2.34 0 0 1-4.659 0 2.34 2.34 0 0 0-3.32-1.915 2.34 2.34 0 0 1-2.33-4.033 2.34 2.34 0 0 0 0-3.831A2.34 2.34 0 0 1 6.35 6.051a2.34 2.34 0 0 0 3.319-1.915",key:"1i5ecw"}],["circle",{cx:"12",cy:"12",r:"3",key:"1v7zrd"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Rn=k("square-terminal",[["path",{d:"m7 11 2-2-2-2",key:"1lz0vl"}],["path",{d:"M11 13h4",key:"1p7l4v"}],["rect",{width:"18",height:"18",x:"3",y:"3",rx:"2",ry:"2",key:"1m3agn"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Sn=k("terminal",[["path",{d:"M12 19h8",key:"baeox8"}],["path",{d:"m4 17 6-6-6-6",key:"1yngyt"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Ln=k("users",[["path",{d:"M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2",key:"1yyitq"}],["path",{d:"M16 3.128a4 4 0 0 1 0 7.744",key:"16gr8j"}],["path",{d:"M22 21v-2a4 4 0 0 0-3-3.87",key:"kshegd"}],["circle",{cx:"9",cy:"7",r:"4",key:"nufk8"}]]);/**
 * @license lucide-vue-next v0.563.0 - ISC
 *
 * This source code is licensed under the ISC license.
 * See the LICENSE file in the root directory of this source tree.
 */const Pn=k("x",[["path",{d:"M18 6 6 18",key:"1bl5f8"}],["path",{d:"m6 6 12 12",key:"d8bk6v"}]]),ce=["top","right","bottom","left"],U=Math.min,S=Math.max,lt=Math.round,ct=Math.floor,z=t=>({x:t,y:t}),le={left:"right",right:"left",bottom:"top",top:"bottom"},ae={start:"end",end:"start"};function wt(t,e,n){return S(t,U(e,n))}function W(t,e){return typeof t=="function"?t(e):t}function N(t){return t.split("-")[0]}function nt(t){return t.split("-")[1]}function kt(t){return t==="x"?"y":"x"}function At(t){return t==="y"?"height":"width"}const fe=new Set(["top","bottom"]);function H(t){return fe.has(N(t))?"y":"x"}function Mt(t){return kt(H(t))}function ue(t,e,n){n===void 0&&(n=!1);const o=nt(t),i=Mt(t),s=At(i);let r=i==="x"?o===(n?"end":"start")?"right":"left":o==="start"?"bottom":"top";return e.reference[s]>e.floating[s]&&(r=at(r)),[r,at(r)]}function de(t){const e=at(t);return[xt(t),e,xt(e)]}function xt(t){return t.replace(/start|end/g,e=>ae[e])}const Et=["left","right"],Tt=["right","left"],he=["top","bottom"],me=["bottom","top"];function pe(t,e,n){switch(t){case"top":case"bottom":return n?e?Tt:Et:e?Et:Tt;case"left":case"right":return e?he:me;default:return[]}}function ye(t,e,n,o){const i=nt(t);let s=pe(N(t),n==="start",o);return i&&(s=s.map(r=>r+"-"+i),e&&(s=s.concat(s.map(xt)))),s}function at(t){return t.replace(/left|right|bottom|top/g,e=>le[e])}function ge(t){return{top:0,right:0,bottom:0,left:0,...t}}function Wt(t){return typeof t!="number"?ge(t):{top:t,right:t,bottom:t,left:t}}function ft(t){const{x:e,y:n,width:o,height:i}=t;return{width:o,height:i,top:n,left:e,right:e+o,bottom:n+i,x:e,y:n}}function Dt(t,e,n){let{reference:o,floating:i}=t;const s=H(e),r=Mt(e),c=At(r),f=N(e),l=s==="y",a=o.x+o.width/2-i.width/2,d=o.y+o.height/2-i.height/2,h=o[c]/2-i[c]/2;let u;switch(f){case"top":u={x:a,y:o.y-i.height};break;case"bottom":u={x:a,y:o.y+o.height};break;case"right":u={x:o.x+o.width,y:d};break;case"left":u={x:o.x-i.width,y:d};break;default:u={x:o.x,y:o.y}}switch(nt(e)){case"start":u[r]-=h*(n&&l?-1:1);break;case"end":u[r]+=h*(n&&l?-1:1);break}return u}async function we(t,e){var n;e===void 0&&(e={});const{x:o,y:i,platform:s,rects:r,elements:c,strategy:f}=t,{boundary:l="clippingAncestors",rootBoundary:a="viewport",elementContext:d="floating",altBoundary:h=!1,padding:u=0}=W(e,t),m=Wt(u),y=c[h?d==="floating"?"reference":"floating":d],g=ft(await s.getClippingRect({element:(n=await(s.isElement==null?void 0:s.isElement(y)))==null||n?y:y.contextElement||await(s.getDocumentElement==null?void 0:s.getDocumentElement(c.floating)),boundary:l,rootBoundary:a,strategy:f})),x=d==="floating"?{x:o,y:i,width:r.floating.width,height:r.floating.height}:r.reference,w=await(s.getOffsetParent==null?void 0:s.getOffsetParent(c.floating)),v=await(s.isElement==null?void 0:s.isElement(w))?await(s.getScale==null?void 0:s.getScale(w))||{x:1,y:1}:{x:1,y:1},A=ft(s.convertOffsetParentRelativeRectToViewportRelativeRect?await s.convertOffsetParentRelativeRectToViewportRelativeRect({elements:c,rect:x,offsetParent:w,strategy:f}):x);return{top:(g.top-A.top+m.top)/v.y,bottom:(A.bottom-g.bottom+m.bottom)/v.y,left:(g.left-A.left+m.left)/v.x,right:(A.right-g.right+m.right)/v.x}}const xe=async(t,e,n)=>{const{placement:o="bottom",strategy:i="absolute",middleware:s=[],platform:r}=n,c=s.filter(Boolean),f=await(r.isRTL==null?void 0:r.isRTL(e));let l=await r.getElementRects({reference:t,floating:e,strategy:i}),{x:a,y:d}=Dt(l,o,f),h=o,u={},m=0;for(let y=0;y<c.length;y++){var p;const{name:g,fn:x}=c[y],{x:w,y:v,data:A,reset:M}=await x({x:a,y:d,initialPlacement:o,placement:h,strategy:i,middlewareData:u,rects:l,platform:{...r,detectOverflow:(p=r.detectOverflow)!=null?p:we},elements:{reference:t,floating:e}});a=w??a,d=v??d,u={...u,[g]:{...u[g],...A}},M&&m<=50&&(m++,typeof M=="object"&&(M.placement&&(h=M.placement),M.rects&&(l=M.rects===!0?await r.getElementRects({reference:t,floating:e,strategy:i}):M.rects),{x:a,y:d}=Dt(l,h,f)),y=-1)}return{x:a,y:d,placement:h,strategy:i,middlewareData:u}},ve=t=>({name:"arrow",options:t,async fn(e){const{x:n,y:o,placement:i,rects:s,platform:r,elements:c,middlewareData:f}=e,{element:l,padding:a=0}=W(t,e)||{};if(l==null)return{};const d=Wt(a),h={x:n,y:o},u=Mt(i),m=At(u),p=await r.getDimensions(l),y=u==="y",g=y?"top":"left",x=y?"bottom":"right",w=y?"clientHeight":"clientWidth",v=s.reference[m]+s.reference[u]-h[u]-s.floating[m],A=h[u]-s.reference[u],M=await(r.getOffsetParent==null?void 0:r.getOffsetParent(l));let b=M?M[w]:0;(!b||!await(r.isElement==null?void 0:r.isElement(M)))&&(b=c.floating[w]||s.floating[m]);const C=v/2-A/2,R=b/2-p[m]/2-1,P=U(d[g],R),q=U(d[x],R),F=P,_=b-p[m]-q,O=b/2-p[m]/2+C,Z=wt(F,O,_),$=!f.arrow&&nt(i)!=null&&O!==Z&&s.reference[m]/2-(O<F?P:q)-p[m]/2<0,E=$?O<F?O-F:O-_:0;return{[u]:h[u]+E,data:{[u]:Z,centerOffset:O-Z-E,...$&&{alignmentOffset:E}},reset:$}}}),be=function(t){return t===void 0&&(t={}),{name:"flip",options:t,async fn(e){var n,o;const{placement:i,middlewareData:s,rects:r,initialPlacement:c,platform:f,elements:l}=e,{mainAxis:a=!0,crossAxis:d=!0,fallbackPlacements:h,fallbackStrategy:u="bestFit",fallbackAxisSideDirection:m="none",flipAlignment:p=!0,...y}=W(t,e);if((n=s.arrow)!=null&&n.alignmentOffset)return{};const g=N(i),x=H(c),w=N(c)===c,v=await(f.isRTL==null?void 0:f.isRTL(l.floating)),A=h||(w||!p?[at(c)]:de(c)),M=m!=="none";!h&&M&&A.push(...ye(c,p,m,v));const b=[c,...A],C=await f.detectOverflow(e,y),R=[];let P=((o=s.flip)==null?void 0:o.overflows)||[];if(a&&R.push(C[g]),d){const O=ue(i,r,v);R.push(C[O[0]],C[O[1]])}if(P=[...P,{placement:i,overflows:R}],!R.every(O=>O<=0)){var q,F;const O=(((q=s.flip)==null?void 0:q.index)||0)+1,Z=b[O];if(Z&&(!(d==="alignment"?x!==H(Z):!1)||P.every(T=>H(T.placement)===x?T.overflows[0]>0:!0)))return{data:{index:O,overflows:P},reset:{placement:Z}};let $=(F=P.filter(E=>E.overflows[0]<=0).sort((E,T)=>E.overflows[1]-T.overflows[1])[0])==null?void 0:F.placement;if(!$)switch(u){case"bestFit":{var _;const E=(_=P.filter(T=>{if(M){const I=H(T.placement);return I===x||I==="y"}return!0}).map(T=>[T.placement,T.overflows.filter(I=>I>0).reduce((I,Kt)=>I+Kt,0)]).sort((T,I)=>T[1]-I[1])[0])==null?void 0:_[0];E&&($=E);break}case"initialPlacement":$=c;break}if(i!==$)return{reset:{placement:$}}}return{}}}};function Vt(t,e){return{top:t.top-e.height,right:t.right-e.width,bottom:t.bottom-e.height,left:t.left-e.width}}function Ft(t){return ce.some(e=>t[e]>=0)}const ke=function(t){return t===void 0&&(t={}),{name:"hide",options:t,async fn(e){const{rects:n,platform:o}=e,{strategy:i="referenceHidden",...s}=W(t,e);switch(i){case"referenceHidden":{const r=await o.detectOverflow(e,{...s,elementContext:"reference"}),c=Vt(r,n.reference);return{data:{referenceHiddenOffsets:c,referenceHidden:Ft(c)}}}case"escaped":{const r=await o.detectOverflow(e,{...s,altBoundary:!0}),c=Vt(r,n.floating);return{data:{escapedOffsets:c,escaped:Ft(c)}}}default:return{}}}}},Nt=new Set(["left","top"]);async function Ae(t,e){const{placement:n,platform:o,elements:i}=t,s=await(o.isRTL==null?void 0:o.isRTL(i.floating)),r=N(n),c=nt(n),f=H(n)==="y",l=Nt.has(r)?-1:1,a=s&&f?-1:1,d=W(e,t);let{mainAxis:h,crossAxis:u,alignmentAxis:m}=typeof d=="number"?{mainAxis:d,crossAxis:0,alignmentAxis:null}:{mainAxis:d.mainAxis||0,crossAxis:d.crossAxis||0,alignmentAxis:d.alignmentAxis};return c&&typeof m=="number"&&(u=c==="end"?m*-1:m),f?{x:u*a,y:h*l}:{x:h*l,y:u*a}}const Me=function(t){return t===void 0&&(t=0),{name:"offset",options:t,async fn(e){var n,o;const{x:i,y:s,placement:r,middlewareData:c}=e,f=await Ae(e,t);return r===((n=c.offset)==null?void 0:n.placement)&&(o=c.arrow)!=null&&o.alignmentOffset?{}:{x:i+f.x,y:s+f.y,data:{...f,placement:r}}}}},Ce=function(t){return t===void 0&&(t={}),{name:"shift",options:t,async fn(e){const{x:n,y:o,placement:i,platform:s}=e,{mainAxis:r=!0,crossAxis:c=!1,limiter:f={fn:g=>{let{x,y:w}=g;return{x,y:w}}},...l}=W(t,e),a={x:n,y:o},d=await s.detectOverflow(e,l),h=H(N(i)),u=kt(h);let m=a[u],p=a[h];if(r){const g=u==="y"?"top":"left",x=u==="y"?"bottom":"right",w=m+d[g],v=m-d[x];m=wt(w,m,v)}if(c){const g=h==="y"?"top":"left",x=h==="y"?"bottom":"right",w=p+d[g],v=p-d[x];p=wt(w,p,v)}const y=f.fn({...e,[u]:m,[h]:p});return{...y,data:{x:y.x-n,y:y.y-o,enabled:{[u]:r,[h]:c}}}}}},Oe=function(t){return t===void 0&&(t={}),{options:t,fn(e){const{x:n,y:o,placement:i,rects:s,middlewareData:r}=e,{offset:c=0,mainAxis:f=!0,crossAxis:l=!0}=W(t,e),a={x:n,y:o},d=H(i),h=kt(d);let u=a[h],m=a[d];const p=W(c,e),y=typeof p=="number"?{mainAxis:p,crossAxis:0}:{mainAxis:0,crossAxis:0,...p};if(f){const w=h==="y"?"height":"width",v=s.reference[h]-s.floating[w]+y.mainAxis,A=s.reference[h]+s.reference[w]-y.mainAxis;u<v?u=v:u>A&&(u=A)}if(l){var g,x;const w=h==="y"?"width":"height",v=Nt.has(N(i)),A=s.reference[d]-s.floating[w]+(v&&((g=r.offset)==null?void 0:g[d])||0)+(v?0:y.crossAxis),M=s.reference[d]+s.reference[w]+(v?0:((x=r.offset)==null?void 0:x[d])||0)-(v?y.crossAxis:0);m<A?m=A:m>M&&(m=M)}return{[h]:u,[d]:m}}}},Re=function(t){return t===void 0&&(t={}),{name:"size",options:t,async fn(e){var n,o;const{placement:i,rects:s,platform:r,elements:c}=e,{apply:f=()=>{},...l}=W(t,e),a=await r.detectOverflow(e,l),d=N(i),h=nt(i),u=H(i)==="y",{width:m,height:p}=s.floating;let y,g;d==="top"||d==="bottom"?(y=d,g=h===(await(r.isRTL==null?void 0:r.isRTL(c.floating))?"start":"end")?"left":"right"):(g=d,y=h==="end"?"top":"bottom");const x=p-a.top-a.bottom,w=m-a.left-a.right,v=U(p-a[y],x),A=U(m-a[g],w),M=!e.middlewareData.shift;let b=v,C=A;if((n=e.middlewareData.shift)!=null&&n.enabled.x&&(C=w),(o=e.middlewareData.shift)!=null&&o.enabled.y&&(b=x),M&&!h){const P=S(a.left,0),q=S(a.right,0),F=S(a.top,0),_=S(a.bottom,0);u?C=m-2*(P!==0||q!==0?P+q:S(a.left,a.right)):b=p-2*(F!==0||_!==0?F+_:S(a.top,a.bottom))}await f({...e,availableWidth:C,availableHeight:b});const R=await r.getDimensions(c.floating);return m!==R.width||p!==R.height?{reset:{rects:!0}}:{}}}};function ut(){return typeof window<"u"}function K(t){return Ct(t)?(t.nodeName||"").toLowerCase():"#document"}function L(t){var e;return(t==null||(e=t.ownerDocument)==null?void 0:e.defaultView)||window}function j(t){var e;return(e=(Ct(t)?t.ownerDocument:t.document)||window.document)==null?void 0:e.documentElement}function Ct(t){return ut()?t instanceof Node||t instanceof L(t).Node:!1}function D(t){return ut()?t instanceof Element||t instanceof L(t).Element:!1}function B(t){return ut()?t instanceof HTMLElement||t instanceof L(t).HTMLElement:!1}function Ht(t){return!ut()||typeof ShadowRoot>"u"?!1:t instanceof ShadowRoot||t instanceof L(t).ShadowRoot}const Se=new Set(["inline","contents"]);function st(t){const{overflow:e,overflowX:n,overflowY:o,display:i}=V(t);return/auto|scroll|overlay|hidden|clip/.test(e+o+n)&&!Se.has(i)}const Le=new Set(["table","td","th"]);function Pe(t){return Le.has(K(t))}const Ee=[":popover-open",":modal"];function dt(t){return Ee.some(e=>{try{return t.matches(e)}catch{return!1}})}const Te=["transform","translate","scale","rotate","perspective"],De=["transform","translate","scale","rotate","perspective","filter"],Ve=["paint","layout","strict","content"];function Ot(t){const e=Rt(),n=D(t)?V(t):t;return Te.some(o=>n[o]?n[o]!=="none":!1)||(n.containerType?n.containerType!=="normal":!1)||!e&&(n.backdropFilter?n.backdropFilter!=="none":!1)||!e&&(n.filter?n.filter!=="none":!1)||De.some(o=>(n.willChange||"").includes(o))||Ve.some(o=>(n.contain||"").includes(o))}function Fe(t){let e=Y(t);for(;B(e)&&!et(e);){if(Ot(e))return e;if(dt(e))return null;e=Y(e)}return null}function Rt(){return typeof CSS>"u"||!CSS.supports?!1:CSS.supports("-webkit-backdrop-filter","none")}const He=new Set(["html","body","#document"]);function et(t){return He.has(K(t))}function V(t){return L(t).getComputedStyle(t)}function ht(t){return D(t)?{scrollLeft:t.scrollLeft,scrollTop:t.scrollTop}:{scrollLeft:t.scrollX,scrollTop:t.scrollY}}function Y(t){if(K(t)==="html")return t;const e=t.assignedSlot||t.parentNode||Ht(t)&&t.host||j(t);return Ht(e)?e.host:e}function qt(t){const e=Y(t);return et(e)?t.ownerDocument?t.ownerDocument.body:t.body:B(e)&&st(e)?e:qt(e)}function rt(t,e,n){var o;e===void 0&&(e=[]),n===void 0&&(n=!0);const i=qt(t),s=i===((o=t.ownerDocument)==null?void 0:o.body),r=L(i);if(s){const c=vt(r);return e.concat(r,r.visualViewport||[],st(i)?i:[],c&&n?rt(c):[])}return e.concat(i,rt(i,[],n))}function vt(t){return t.parent&&Object.getPrototypeOf(t.parent)?t.frameElement:null}function _t(t){const e=V(t);let n=parseFloat(e.width)||0,o=parseFloat(e.height)||0;const i=B(t),s=i?t.offsetWidth:n,r=i?t.offsetHeight:o,c=lt(n)!==s||lt(o)!==r;return c&&(n=s,o=r),{width:n,height:o,$:c}}function St(t){return D(t)?t:t.contextElement}function tt(t){const e=St(t);if(!B(e))return z(1);const n=e.getBoundingClientRect(),{width:o,height:i,$:s}=_t(e);let r=(s?lt(n.width):n.width)/o,c=(s?lt(n.height):n.height)/i;return(!r||!Number.isFinite(r))&&(r=1),(!c||!Number.isFinite(c))&&(c=1),{x:r,y:c}}const ze=z(0);function It(t){const e=L(t);return!Rt()||!e.visualViewport?ze:{x:e.visualViewport.offsetLeft,y:e.visualViewport.offsetTop}}function Be(t,e,n){return e===void 0&&(e=!1),!n||e&&n!==L(t)?!1:e}function G(t,e,n,o){e===void 0&&(e=!1),n===void 0&&(n=!1);const i=t.getBoundingClientRect(),s=St(t);let r=z(1);e&&(o?D(o)&&(r=tt(o)):r=tt(t));const c=Be(s,n,o)?It(s):z(0);let f=(i.left+c.x)/r.x,l=(i.top+c.y)/r.y,a=i.width/r.x,d=i.height/r.y;if(s){const h=L(s),u=o&&D(o)?L(o):o;let m=h,p=vt(m);for(;p&&o&&u!==m;){const y=tt(p),g=p.getBoundingClientRect(),x=V(p),w=g.left+(p.clientLeft+parseFloat(x.paddingLeft))*y.x,v=g.top+(p.clientTop+parseFloat(x.paddingTop))*y.y;f*=y.x,l*=y.y,a*=y.x,d*=y.y,f+=w,l+=v,m=L(p),p=vt(m)}}return ft({width:a,height:d,x:f,y:l})}function mt(t,e){const n=ht(t).scrollLeft;return e?e.left+n:G(j(t)).left+n}function Xt(t,e){const n=t.getBoundingClientRect(),o=n.left+e.scrollLeft-mt(t,n),i=n.top+e.scrollTop;return{x:o,y:i}}function je(t){let{elements:e,rect:n,offsetParent:o,strategy:i}=t;const s=i==="fixed",r=j(o),c=e?dt(e.floating):!1;if(o===r||c&&s)return n;let f={scrollLeft:0,scrollTop:0},l=z(1);const a=z(0),d=B(o);if((d||!d&&!s)&&((K(o)!=="body"||st(r))&&(f=ht(o)),B(o))){const u=G(o);l=tt(o),a.x=u.x+o.clientLeft,a.y=u.y+o.clientTop}const h=r&&!d&&!s?Xt(r,f):z(0);return{width:n.width*l.x,height:n.height*l.y,x:n.x*l.x-f.scrollLeft*l.x+a.x+h.x,y:n.y*l.y-f.scrollTop*l.y+a.y+h.y}}function $e(t){return Array.from(t.getClientRects())}function We(t){const e=j(t),n=ht(t),o=t.ownerDocument.body,i=S(e.scrollWidth,e.clientWidth,o.scrollWidth,o.clientWidth),s=S(e.scrollHeight,e.clientHeight,o.scrollHeight,o.clientHeight);let r=-n.scrollLeft+mt(t);const c=-n.scrollTop;return V(o).direction==="rtl"&&(r+=S(e.clientWidth,o.clientWidth)-i),{width:i,height:s,x:r,y:c}}const zt=25;function Ne(t,e){const n=L(t),o=j(t),i=n.visualViewport;let s=o.clientWidth,r=o.clientHeight,c=0,f=0;if(i){s=i.width,r=i.height;const a=Rt();(!a||a&&e==="fixed")&&(c=i.offsetLeft,f=i.offsetTop)}const l=mt(o);if(l<=0){const a=o.ownerDocument,d=a.body,h=getComputedStyle(d),u=a.compatMode==="CSS1Compat"&&parseFloat(h.marginLeft)+parseFloat(h.marginRight)||0,m=Math.abs(o.clientWidth-d.clientWidth-u);m<=zt&&(s-=m)}else l<=zt&&(s+=l);return{width:s,height:r,x:c,y:f}}const qe=new Set(["absolute","fixed"]);function _e(t,e){const n=G(t,!0,e==="fixed"),o=n.top+t.clientTop,i=n.left+t.clientLeft,s=B(t)?tt(t):z(1),r=t.clientWidth*s.x,c=t.clientHeight*s.y,f=i*s.x,l=o*s.y;return{width:r,height:c,x:f,y:l}}function Bt(t,e,n){let o;if(e==="viewport")o=Ne(t,n);else if(e==="document")o=We(j(t));else if(D(e))o=_e(e,n);else{const i=It(t);o={x:e.x-i.x,y:e.y-i.y,width:e.width,height:e.height}}return ft(o)}function Ut(t,e){const n=Y(t);return n===e||!D(n)||et(n)?!1:V(n).position==="fixed"||Ut(n,e)}function Ie(t,e){const n=e.get(t);if(n)return n;let o=rt(t,[],!1).filter(c=>D(c)&&K(c)!=="body"),i=null;const s=V(t).position==="fixed";let r=s?Y(t):t;for(;D(r)&&!et(r);){const c=V(r),f=Ot(r);!f&&c.position==="fixed"&&(i=null),(s?!f&&!i:!f&&c.position==="static"&&!!i&&qe.has(i.position)||st(r)&&!f&&Ut(t,r))?o=o.filter(a=>a!==r):i=c,r=Y(r)}return e.set(t,o),o}function Xe(t){let{element:e,boundary:n,rootBoundary:o,strategy:i}=t;const r=[...n==="clippingAncestors"?dt(e)?[]:Ie(e,this._c):[].concat(n),o],c=r[0],f=r.reduce((l,a)=>{const d=Bt(e,a,i);return l.top=S(d.top,l.top),l.right=U(d.right,l.right),l.bottom=U(d.bottom,l.bottom),l.left=S(d.left,l.left),l},Bt(e,c,i));return{width:f.right-f.left,height:f.bottom-f.top,x:f.left,y:f.top}}function Ue(t){const{width:e,height:n}=_t(t);return{width:e,height:n}}function Ye(t,e,n){const o=B(e),i=j(e),s=n==="fixed",r=G(t,!0,s,e);let c={scrollLeft:0,scrollTop:0};const f=z(0);function l(){f.x=mt(i)}if(o||!o&&!s)if((K(e)!=="body"||st(i))&&(c=ht(e)),o){const u=G(e,!0,s,e);f.x=u.x+e.clientLeft,f.y=u.y+e.clientTop}else i&&l();s&&!o&&i&&l();const a=i&&!o&&!s?Xt(i,c):z(0),d=r.left+c.scrollLeft-f.x-a.x,h=r.top+c.scrollTop-f.y-a.y;return{x:d,y:h,width:r.width,height:r.height}}function yt(t){return V(t).position==="static"}function jt(t,e){if(!B(t)||V(t).position==="fixed")return null;if(e)return e(t);let n=t.offsetParent;return j(t)===n&&(n=n.ownerDocument.body),n}function Yt(t,e){const n=L(t);if(dt(t))return n;if(!B(t)){let i=Y(t);for(;i&&!et(i);){if(D(i)&&!yt(i))return i;i=Y(i)}return n}let o=jt(t,e);for(;o&&Pe(o)&&yt(o);)o=jt(o,e);return o&&et(o)&&yt(o)&&!Ot(o)?n:o||Fe(t)||n}const Ze=async function(t){const e=this.getOffsetParent||Yt,n=this.getDimensions,o=await n(t.floating);return{reference:Ye(t.reference,await e(t.floating),t.strategy),floating:{x:0,y:0,width:o.width,height:o.height}}};function Ge(t){return V(t).direction==="rtl"}const Ke={convertOffsetParentRelativeRectToViewportRelativeRect:je,getDocumentElement:j,getClippingRect:Xe,getOffsetParent:Yt,getElementRects:Ze,getClientRects:$e,getDimensions:Ue,getScale:tt,isElement:D,isRTL:Ge};function Zt(t,e){return t.x===e.x&&t.y===e.y&&t.width===e.width&&t.height===e.height}function Je(t,e){let n=null,o;const i=j(t);function s(){var c;clearTimeout(o),(c=n)==null||c.disconnect(),n=null}function r(c,f){c===void 0&&(c=!1),f===void 0&&(f=1),s();const l=t.getBoundingClientRect(),{left:a,top:d,width:h,height:u}=l;if(c||e(),!h||!u)return;const m=ct(d),p=ct(i.clientWidth-(a+h)),y=ct(i.clientHeight-(d+u)),g=ct(a),w={rootMargin:-m+"px "+-p+"px "+-y+"px "+-g+"px",threshold:S(0,U(1,f))||1};let v=!0;function A(M){const b=M[0].intersectionRatio;if(b!==f){if(!v)return r();b?r(!1,b):o=setTimeout(()=>{r(!1,1e-7)},1e3)}b===1&&!Zt(l,t.getBoundingClientRect())&&r(),v=!1}try{n=new IntersectionObserver(A,{...w,root:i.ownerDocument})}catch{n=new IntersectionObserver(A,w)}n.observe(t)}return r(!0),s}function En(t,e,n,o){o===void 0&&(o={});const{ancestorScroll:i=!0,ancestorResize:s=!0,elementResize:r=typeof ResizeObserver=="function",layoutShift:c=typeof IntersectionObserver=="function",animationFrame:f=!1}=o,l=St(t),a=i||s?[...l?rt(l):[],...rt(e)]:[];a.forEach(g=>{i&&g.addEventListener("scroll",n,{passive:!0}),s&&g.addEventListener("resize",n)});const d=l&&c?Je(l,n):null;let h=-1,u=null;r&&(u=new ResizeObserver(g=>{let[x]=g;x&&x.target===l&&u&&(u.unobserve(e),cancelAnimationFrame(h),h=requestAnimationFrame(()=>{var w;(w=u)==null||w.observe(e)})),n()}),l&&!f&&u.observe(l),u.observe(e));let m,p=f?G(t):null;f&&y();function y(){const g=G(t);p&&!Zt(p,g)&&n(),p=g,m=requestAnimationFrame(y)}return n(),()=>{var g;a.forEach(x=>{i&&x.removeEventListener("scroll",n),s&&x.removeEventListener("resize",n)}),d==null||d(),(g=u)==null||g.disconnect(),u=null,f&&cancelAnimationFrame(m)}}const Tn=Me,Dn=Ce,Vn=be,Fn=Re,Hn=ke,Qe=ve,zn=Oe,tn=(t,e,n)=>{const o=new Map,i={platform:Ke,...n},s={...i.platform,_c:o};return xe(t,e,{...i,platform:s})};function en(t){return t!=null&&typeof t=="object"&&"$el"in t}function bt(t){if(en(t)){const e=t.$el;return Ct(e)&&K(e)==="#comment"?null:e}return t}function Q(t){return typeof t=="function"?t():ee(t)}function Bn(t){return{name:"arrow",options:t,fn(e){const n=bt(Q(t.element));return n==null?{}:Qe({element:n,padding:t.padding}).fn(e)}}}function Gt(t){return typeof window>"u"?1:(t.ownerDocument.defaultView||window).devicePixelRatio||1}function $t(t,e){const n=Gt(t);return Math.round(e*n)/n}function jn(t,e,n){n===void 0&&(n={});const o=n.whileElementsMounted,i=X(()=>{var b;return(b=Q(n.open))!=null?b:!0}),s=X(()=>Q(n.middleware)),r=X(()=>{var b;return(b=Q(n.placement))!=null?b:"bottom"}),c=X(()=>{var b;return(b=Q(n.strategy))!=null?b:"absolute"}),f=X(()=>{var b;return(b=Q(n.transform))!=null?b:!0}),l=X(()=>bt(t.value)),a=X(()=>bt(e.value)),d=ot(0),h=ot(0),u=ot(c.value),m=ot(r.value),p=Jt({}),y=ot(!1),g=X(()=>{const b={position:u.value,left:"0",top:"0"};if(!a.value)return b;const C=$t(a.value,d.value),R=$t(a.value,h.value);return f.value?{...b,transform:"translate("+C+"px, "+R+"px)",...Gt(a.value)>=1.5&&{willChange:"transform"}}:{position:u.value,left:C+"px",top:R+"px"}});let x;function w(){if(l.value==null||a.value==null)return;const b=i.value;tn(l.value,a.value,{middleware:s.value,placement:r.value,strategy:c.value}).then(C=>{d.value=C.x,h.value=C.y,u.value=C.strategy,m.value=C.placement,p.value=C.middlewareData,y.value=b!==!1})}function v(){typeof x=="function"&&(x(),x=void 0)}function A(){if(v(),o===void 0){w();return}if(l.value!=null&&a.value!=null){x=o(l.value,a.value,w);return}}function M(){i.value||(y.value=!1)}return pt([s,r,c,i],w,{flush:"sync"}),pt([l,a],A,{flush:"sync"}),pt(i,M,{flush:"sync"}),Qt()&&te(v),{x:J(d),y:J(h),strategy:J(u),placement:J(m),middlewareData:J(p),isPositioned:J(y),floatingStyles:g,update:w}}export{on as B,dn as C,hn as E,pn as F,yn as G,gn as I,wn as L,xn as M,vn as P,On as S,Sn as T,Ln as U,Pn as X,En as a,Fn as b,Bn as c,rn as d,sn as e,Vn as f,mn as g,Hn as h,Rn as i,fn as j,Cn as k,zn as l,bn as m,kn as n,Tn as o,An as p,ln as q,an as r,Dn as s,cn as t,jn as u,Mn as v,un as w};
