var e={MEDIA_PLAY_REQUEST:`mediaplayrequest`,MEDIA_PAUSE_REQUEST:`mediapauserequest`,MEDIA_MUTE_REQUEST:`mediamuterequest`,MEDIA_UNMUTE_REQUEST:`mediaunmuterequest`,MEDIA_LOOP_REQUEST:`medialooprequest`,MEDIA_VOLUME_REQUEST:`mediavolumerequest`,MEDIA_SEEK_REQUEST:`mediaseekrequest`,MEDIA_AIRPLAY_REQUEST:`mediaairplayrequest`,MEDIA_ENTER_FULLSCREEN_REQUEST:`mediaenterfullscreenrequest`,MEDIA_EXIT_FULLSCREEN_REQUEST:`mediaexitfullscreenrequest`,MEDIA_PREVIEW_REQUEST:`mediapreviewrequest`,MEDIA_ENTER_PIP_REQUEST:`mediaenterpiprequest`,MEDIA_EXIT_PIP_REQUEST:`mediaexitpiprequest`,MEDIA_ENTER_CAST_REQUEST:`mediaentercastrequest`,MEDIA_EXIT_CAST_REQUEST:`mediaexitcastrequest`,MEDIA_SHOW_TEXT_TRACKS_REQUEST:`mediashowtexttracksrequest`,MEDIA_HIDE_TEXT_TRACKS_REQUEST:`mediahidetexttracksrequest`,MEDIA_SHOW_SUBTITLES_REQUEST:`mediashowsubtitlesrequest`,MEDIA_DISABLE_SUBTITLES_REQUEST:`mediadisablesubtitlesrequest`,MEDIA_TOGGLE_SUBTITLES_REQUEST:`mediatogglesubtitlesrequest`,MEDIA_PLAYBACK_RATE_REQUEST:`mediaplaybackraterequest`,MEDIA_RENDITION_REQUEST:`mediarenditionrequest`,MEDIA_AUDIO_TRACK_REQUEST:`mediaaudiotrackrequest`,MEDIA_SEEK_TO_LIVE_REQUEST:`mediaseektoliverequest`,REGISTER_MEDIA_STATE_RECEIVER:`registermediastatereceiver`,UNREGISTER_MEDIA_STATE_RECEIVER:`unregistermediastatereceiver`},t={MEDIA_CHROME_ATTRIBUTES:`mediachromeattributes`,MEDIA_CONTROLLER:`mediacontroller`},n={MEDIA_AIRPLAY_UNAVAILABLE:`mediaAirplayUnavailable`,MEDIA_AUDIO_TRACK_ENABLED:`mediaAudioTrackEnabled`,MEDIA_AUDIO_TRACK_LIST:`mediaAudioTrackList`,MEDIA_AUDIO_TRACK_UNAVAILABLE:`mediaAudioTrackUnavailable`,MEDIA_BUFFERED:`mediaBuffered`,MEDIA_CAST_UNAVAILABLE:`mediaCastUnavailable`,MEDIA_CHAPTERS_CUES:`mediaChaptersCues`,MEDIA_CURRENT_TIME:`mediaCurrentTime`,MEDIA_DURATION:`mediaDuration`,MEDIA_ENDED:`mediaEnded`,MEDIA_ERROR:`mediaError`,MEDIA_ERROR_CODE:`mediaErrorCode`,MEDIA_ERROR_MESSAGE:`mediaErrorMessage`,MEDIA_FULLSCREEN_UNAVAILABLE:`mediaFullscreenUnavailable`,MEDIA_HAS_PLAYED:`mediaHasPlayed`,MEDIA_HEIGHT:`mediaHeight`,MEDIA_IS_AIRPLAYING:`mediaIsAirplaying`,MEDIA_IS_CASTING:`mediaIsCasting`,MEDIA_IS_FULLSCREEN:`mediaIsFullscreen`,MEDIA_IS_PIP:`mediaIsPip`,MEDIA_LOADING:`mediaLoading`,MEDIA_MUTED:`mediaMuted`,MEDIA_LOOP:`mediaLoop`,MEDIA_PAUSED:`mediaPaused`,MEDIA_PIP_UNAVAILABLE:`mediaPipUnavailable`,MEDIA_PLAYBACK_RATE:`mediaPlaybackRate`,MEDIA_PREVIEW_CHAPTER:`mediaPreviewChapter`,MEDIA_PREVIEW_COORDS:`mediaPreviewCoords`,MEDIA_PREVIEW_IMAGE:`mediaPreviewImage`,MEDIA_PREVIEW_TIME:`mediaPreviewTime`,MEDIA_RENDITION_LIST:`mediaRenditionList`,MEDIA_RENDITION_SELECTED:`mediaRenditionSelected`,MEDIA_RENDITION_UNAVAILABLE:`mediaRenditionUnavailable`,MEDIA_SEEKABLE:`mediaSeekable`,MEDIA_STREAM_TYPE:`mediaStreamType`,MEDIA_SUBTITLES_LIST:`mediaSubtitlesList`,MEDIA_SUBTITLES_SHOWING:`mediaSubtitlesShowing`,MEDIA_TARGET_LIVE_WINDOW:`mediaTargetLiveWindow`,MEDIA_TIME_IS_LIVE:`mediaTimeIsLive`,MEDIA_VOLUME:`mediaVolume`,MEDIA_VOLUME_LEVEL:`mediaVolumeLevel`,MEDIA_VOLUME_UNAVAILABLE:`mediaVolumeUnavailable`,MEDIA_LANG:`mediaLang`,MEDIA_WIDTH:`mediaWidth`},r=Object.entries(n),i=r.reduce((e,[t,n])=>(e[t]=n.toLowerCase(),e),{}),a=r.reduce((e,[t,n])=>(e[t]=n.toLowerCase(),e),{USER_INACTIVE_CHANGE:`userinactivechange`,BREAKPOINTS_CHANGE:`breakpointchange`,BREAKPOINTS_COMPUTED:`breakpointscomputed`});Object.entries(a).reduce((e,[t,n])=>{let r=i[t];return r&&(e[n]=r),e},{userinactivechange:`userinactive`});var o=Object.entries(i).reduce((e,[t,n])=>{let r=a[t];return r&&(e[n]=r),e},{userinactive:`userinactivechange`}),s={SUBTITLES:`subtitles`,CAPTIONS:`captions`,DESCRIPTIONS:`descriptions`,CHAPTERS:`chapters`,METADATA:`metadata`},c={DISABLED:`disabled`,HIDDEN:`hidden`,SHOWING:`showing`},l={MOUSE:`mouse`,PEN:`pen`,TOUCH:`touch`},u={UNAVAILABLE:`unavailable`,UNSUPPORTED:`unsupported`},d={LIVE:`live`,ON_DEMAND:`on-demand`,UNKNOWN:`unknown`},f={INLINE:`inline`,FULLSCREEN:`fullscreen`,PICTURE_IN_PICTURE:`picture-in-picture`};function p(e){return e?.map(m).join(` `)}function m(e){if(e){let{id:t,width:n,height:r}=e;return[t,n,r].filter(e=>e!=null).join(`:`)}}function h(e){return e?.map(ee).join(` `)}function ee(e){if(e){let{id:t,kind:n,language:r,label:i}=e;return[t,n,r,i].filter(e=>e!=null).join(`:`)}}function te(e){return typeof e==`number`&&!Number.isNaN(e)&&Number.isFinite(e)}var ne=e=>new Promise(t=>setTimeout(t,e)),g={en:{"Start airplay":`Start airplay`,"Stop airplay":`Stop airplay`,Audio:`Audio`,Captions:`Captions`,"Enable captions":`Enable captions`,"Disable captions":`Disable captions`,"Start casting":`Start casting`,"Stop casting":`Stop casting`,"Enter fullscreen mode":`Enter fullscreen mode`,"Exit fullscreen mode":`Exit fullscreen mode`,Mute:`Mute`,Unmute:`Unmute`,Loop:`Loop`,"Enter picture in picture mode":`Enter picture in picture mode`,"Exit picture in picture mode":`Exit picture in picture mode`,Play:`Play`,Pause:`Pause`,"Playback rate":`Playback rate`,"Playback rate {playbackRate}":`Playback rate {playbackRate}`,Quality:`Quality`,"Seek backward":`Seek backward`,"Seek forward":`Seek forward`,Settings:`Settings`,Auto:`Auto`,"audio player":`audio player`,"video player":`video player`,volume:`volume`,seek:`seek`,"closed captions":`closed captions`,"current playback rate":`current playback rate`,"playback time":`playback time`,"media loading":`media loading`,settings:`settings`,"audio tracks":`audio tracks`,quality:`quality`,play:`play`,pause:`pause`,mute:`mute`,unmute:`unmute`,"chapter: {chapterName}":`chapter: {chapterName}`,live:`live`,Off:`Off`,"start airplay":`start airplay`,"stop airplay":`stop airplay`,"start casting":`start casting`,"stop casting":`stop casting`,"enter fullscreen mode":`enter fullscreen mode`,"exit fullscreen mode":`exit fullscreen mode`,"enter picture in picture mode":`enter picture in picture mode`,"exit picture in picture mode":`exit picture in picture mode`,"seek to live":`seek to live`,"playing live":`playing live`,"seek back {seekOffset} seconds":`seek back {seekOffset} seconds`,"seek forward {seekOffset} seconds":`seek forward {seekOffset} seconds`,"Network Error":`Network Error`,"Decode Error":`Decode Error`,"Source Not Supported":`Source Not Supported`,"Encryption Error":`Encryption Error`,"A network error caused the media download to fail.":`A network error caused the media download to fail.`,"A media error caused playback to be aborted. The media could be corrupt or your browser does not support this format.":`A media error caused playback to be aborted. The media could be corrupt or your browser does not support this format.`,"An unsupported error occurred. The server or network failed, or your browser does not support this format.":`An unsupported error occurred. The server or network failed, or your browser does not support this format.`,"The media is encrypted and there are no keys to decrypt it.":`The media is encrypted and there are no keys to decrypt it.`,hour:`hour`,hours:`hours`,minute:`minute`,minutes:`minutes`,second:`second`,seconds:`seconds`,"{time} remaining":`{time} remaining`,"{currentTime} of {totalTime}":`{currentTime} of {totalTime}`,"video not loaded, unknown time.":`video not loaded, unknown time.`}},re=globalThis.navigator?.language||`en`,ie=e=>{re=e},ae=(e,t)=>{g[e]=t},oe=e=>{let[t]=re.split(`-`);return g[re]?.[e]||g[t]?.[e]||g.en?.[e]||e},se=()=>{let[e]=re.split(`-`);return g[re]?re:g[e]?e:`en`},_=(e,t={})=>oe(e).replace(/\{(\w+)\}/g,(e,n)=>n in t?String(t[n]):`{${n}}`),ce=[{singular:`hour`,plural:`hours`},{singular:`minute`,plural:`minutes`},{singular:`second`,plural:`seconds`}],le=(e,t)=>`${e} ${_(e===1?ce[t].singular:ce[t].plural)}`,ue=e=>{if(!te(e))return``;let t=Math.abs(e),n=t!==e,r=new Date(0,0,0,0,0,t,0),i=[r.getHours(),r.getMinutes(),r.getSeconds()].map((e,t)=>e&&le(e,t)).filter(e=>e).join(`, `);return n?_(`{time} remaining`,{time:i}):i};function de(e,t){let n=!1;e<0&&(n=!0,e=0-e),e=e<0?0:e;let r=Math.floor(e%60),i=Math.floor(e/60%60),a=Math.floor(e/3600),o=Math.floor(t/60%60),s=Math.floor(t/3600);return(isNaN(e)||e===1/0)&&(a=i=r=`0`),a=a>0||s>0?a+`:`:``,i=((a||o>=10)&&i<10?`0`+i:i)+`:`,r=r<10?`0`+r:r,(n?`-`:``)+a+i+r}Object.freeze({length:0,start(e){let t=e>>>0;if(t>=this.length)throw new DOMException(`Failed to execute 'start' on 'TimeRanges': The index provided (${t}) is greater than or equal to the maximum bound (${this.length}).`);return 0},end(e){let t=e>>>0;if(t>=this.length)throw new DOMException(`Failed to execute 'end' on 'TimeRanges': The index provided (${t}) is greater than or equal to the maximum bound (${this.length}).`);return 0}});var fe=class{addEventListener(){}removeEventListener(){}dispatchEvent(){return!0}},pe=class extends fe{},me=class extends pe{constructor(){super(...arguments),this.role=null}},he=class{observe(){}unobserve(){}disconnect(){}},ge={createElement:function(){return new _e.HTMLElement},createElementNS:function(){return new _e.HTMLElement},addEventListener(){},removeEventListener(){},dispatchEvent(e){return!1}},_e={ResizeObserver:he,document:ge,Node:pe,Element:me,HTMLElement:class extends me{constructor(){super(...arguments),this.innerHTML=``}get content(){return new _e.DocumentFragment}},DocumentFragment:class extends fe{},customElements:{get:function(){},define:function(){},whenDefined:function(){}},localStorage:{getItem(e){return null},setItem(e,t){},removeItem(e){}},CustomEvent:function(){},getComputedStyle:function(){},navigator:{languages:[],get userAgent(){return``}},matchMedia(e){return{matches:!1,media:e}},DOMParser:class{parseFromString(e,t){return{body:{textContent:e}}}}},ve=`global`in globalThis&&(globalThis==null?void 0:globalThis.global)===globalThis||typeof window>`u`||window.customElements===void 0,ye=Object.keys(_e).every(e=>e in globalThis),v=ve&&!ye?_e:globalThis,y=ve&&!ye?ge:globalThis.document,be=new WeakMap,xe=e=>{let t=be.get(e);return t||be.set(e,t=new Set),t},Se=new v.ResizeObserver(e=>{for(let t of e)for(let e of xe(t.target))e(t)});function Ce(e,t){xe(e).add(t),Se.observe(e)}function we(e,t){let n=xe(e);n.delete(t),n.size||Se.unobserve(e)}function b(e){let t={};for(let n of e)t[n.name]=n.value;return t}function Te(e){return Ee(e)??je(e,`media-controller`)}function Ee(e){let{MEDIA_CONTROLLER:n}=t,r=e.getAttribute(n);if(r)return Ne(e)?.getElementById(r)}var De=(e,t,n=`.value`)=>{let r=e.querySelector(n);r&&(r.textContent=t)},Oe=(e,t)=>{let n=`slot[name="${t}"]`,r=e.shadowRoot.querySelector(n);return r?r.children:[]},ke=(e,t)=>Oe(e,t)[0],Ae=(e,t)=>!e||!t?!1:e?.contains(t)?!0:Ae(e,t.getRootNode().host),je=(e,t)=>e?e.closest(t)||je(e.getRootNode().host,t):null;function Me(e=document){let t=e?.activeElement;return t?Me(t.shadowRoot)??t:null}function Ne(e){let t=(e?.getRootNode)?.call(e);return t instanceof ShadowRoot||t instanceof Document?t:null}function Pe(e,{depth:t=3,checkOpacity:n=!0,checkVisibilityCSS:r=!0}={}){if(e.checkVisibility)return e.checkVisibility({checkOpacity:n,checkVisibilityCSS:r});let i=e;for(;i&&t>0;){let e=getComputedStyle(i);if(n&&e.opacity===`0`||r&&e.visibility===`hidden`||e.display===`none`)return!1;i=i.parentElement,t--}return!0}function Fe(e,t,n,r){let i=r.x-n.x,a=r.y-n.y,o=i*i+a*a;if(o===0)return 0;let s=((e-n.x)*i+(t-n.y)*a)/o;return Math.max(0,Math.min(1,s))}function x(e,t){return Ie(e,e=>e===t)||Le(e,t)}function Ie(e,t){let n;for(n of e.querySelectorAll(`style:not([media])`)??[]){let e;try{e=n.sheet?.cssRules}catch{continue}for(let n of e??[])if(t(n.selectorText))return n}}function Le(e,t){let n=e.querySelectorAll(`style:not([media])`)??[],r=n?.[n.length-1];if(!r?.sheet)return console.warn(`Media Chrome: No style sheet found on style tag of`,e),{style:{setProperty:()=>{},removeProperty:()=>``,getPropertyValue:()=>``}};let i=r?.sheet.insertRule(`${t}{}`,r.sheet.cssRules.length);return r.sheet.cssRules?.[i]}function S(e,t,n=NaN){let r=e.getAttribute(t);return r==null?n:+r}function C(e,t,n){let r=+n;if(n==null||Number.isNaN(r)){e.hasAttribute(t)&&e.removeAttribute(t);return}S(e,t,void 0)!==r&&e.setAttribute(t,`${r}`)}function w(e,t){return e.hasAttribute(t)}function T(e,t,n){if(n==null){e.hasAttribute(t)&&e.removeAttribute(t);return}w(e,t)!=n&&e.toggleAttribute(t,n)}function E(e,t,n=null){return e.getAttribute(t)??n}function D(e,t,n){if(n==null){e.hasAttribute(t)&&e.removeAttribute(t);return}let r=`${n}`;E(e,t,void 0)!==r&&e.setAttribute(t,r)}var Re=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},O=(e,t,n)=>(Re(e,t,`read from private field`),n?n.call(e):t.get(e)),ze=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},Be=(e,t,n,r)=>(Re(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),k;function Ve(e){return`
    <style>
      :host {
        display: var(--media-control-display, var(--media-gesture-receiver-display, inline-block));
        box-sizing: border-box;
      }
    </style>
  `}var He=class extends v.HTMLElement{constructor(){if(super(),ze(this,k,void 0),!this.shadowRoot){this.attachShadow(this.constructor.shadowRootOptions);let e=b(this.attributes);this.shadowRoot.innerHTML=this.constructor.getTemplateHTML(e)}}static get observedAttributes(){return[t.MEDIA_CONTROLLER,i.MEDIA_PAUSED]}attributeChangedCallback(e,n,r){var i,a,o,s;e===t.MEDIA_CONTROLLER&&(n&&((a=(i=O(this,k))?.unassociateElement)==null||a.call(i,this),Be(this,k,null)),r&&this.isConnected&&(Be(this,k,this.getRootNode()?.getElementById(r)),(s=(o=O(this,k))?.associateElement)==null||s.call(o,this)))}connectedCallback(){var e,n;this.tabIndex=-1,this.setAttribute(`aria-hidden`,`true`),Be(this,k,Ue(this)),this.getAttribute(t.MEDIA_CONTROLLER)&&((n=(e=O(this,k))?.associateElement)==null||n.call(e,this)),O(this,k)&&(O(this,k).addEventListener(`pointerdown`,this),O(this,k).addEventListener(`click`,this),O(this,k).hasAttribute(`tabindex`)||(O(this,k).tabIndex=0))}disconnectedCallback(){var e,n,r,i;this.getAttribute(t.MEDIA_CONTROLLER)&&((n=(e=O(this,k))?.unassociateElement)==null||n.call(e,this)),(r=O(this,k))==null||r.removeEventListener(`pointerdown`,this),(i=O(this,k))==null||i.removeEventListener(`click`,this),Be(this,k,null)}handleEvent(e){let t=e.composedPath()?.[0];if([`video`,`media-controller`].includes(t?.localName)){if(e.type===`pointerdown`)this._pointerType=e.pointerType;else if(e.type===`click`){let{clientX:t,clientY:n}=e,{left:r,top:i,width:a,height:o}=this.getBoundingClientRect(),s=t-r,c=n-i;if(s<0||c<0||s>a||c>o||a===0&&o===0)return;let u=this._pointerType||`mouse`;if(this._pointerType=void 0,u===l.TOUCH){this.handleTap(e);return}if(u===l.MOUSE||u===l.PEN){this.handleMouseClick(e);return}}}}get mediaPaused(){return w(this,i.MEDIA_PAUSED)}set mediaPaused(e){T(this,i.MEDIA_PAUSED,e)}handleTap(e){}handleMouseClick(t){let n=this.mediaPaused?e.MEDIA_PLAY_REQUEST:e.MEDIA_PAUSE_REQUEST;this.dispatchEvent(new v.CustomEvent(n,{composed:!0,bubbles:!0}))}};k=new WeakMap,He.shadowRootOptions={mode:`open`},He.getTemplateHTML=Ve;function Ue(e){let n=e.getAttribute(t.MEDIA_CONTROLLER);return n?e.getRootNode()?.getElementById(n):je(e,`media-controller`)}v.customElements.get(`media-gesture-receiver`)||v.customElements.define(`media-gesture-receiver`,He);var We=He,Ge=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},A=(e,t,n)=>(Ge(e,t,`read from private field`),n?n.call(e):t.get(e)),j=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},M=(e,t,n,r)=>(Ge(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),N=(e,t,n)=>(Ge(e,t,`access private method`),n),Ke,qe,Je,Ye,Xe,Ze,Qe,$e,et,tt,nt,rt,it,at,ot,st,ct,lt,ut,dt,P={AUDIO:`audio`,AUTOHIDE:`autohide`,BREAKPOINTS:`breakpoints`,GESTURES_DISABLED:`gesturesdisabled`,KEYBOARD_CONTROL:`keyboardcontrol`,NO_AUTOHIDE:`noautohide`,USER_INACTIVE:`userinactive`,AUTOHIDE_OVER_CONTROLS:`autohideovercontrols`};function ft(e){return`
    <style>
      
      :host([${i.MEDIA_IS_FULLSCREEN}]) ::slotted([slot=media]) {
        outline: none;
      }

      :host {
        box-sizing: border-box;
        position: relative;
        display: inline-block;
        line-height: 0;
        background-color: var(--media-background-color, #000);
        overflow: hidden;
      }

      :host(:not([${P.AUDIO}])) [part~=layer]:not([part~=media-layer]) {
        position: absolute;
        top: 0;
        left: 0;
        bottom: 0;
        right: 0;
        display: flex;
        flex-flow: column nowrap;
        align-items: start;
        pointer-events: none;
        background: none;
      }

      slot[name=media] {
        display: var(--media-slot-display, contents);
      }

      
      :host([${P.AUDIO}]) slot[name=media] {
        display: var(--media-slot-display, none);
      }

      
      :host([${P.AUDIO}]) [part~=layer][part~=gesture-layer] {
        height: 0;
        display: block;
      }

      
      :host(:not([${P.AUDIO}])[${P.GESTURES_DISABLED}]) ::slotted([slot=gestures-chrome]),
          :host(:not([${P.AUDIO}])[${P.GESTURES_DISABLED}]) media-gesture-receiver[slot=gestures-chrome] {
        display: none;
      }

      
      ::slotted(:not([slot=media]):not([slot=poster]):not(media-loading-indicator):not([role=dialog]):not([hidden])) {
        pointer-events: auto;
      }

      :host(:not([${P.AUDIO}])) *[part~=layer][part~=centered-layer] {
        align-items: center;
        justify-content: center;
      }

      :host(:not([${P.AUDIO}])) ::slotted(media-gesture-receiver[slot=gestures-chrome]),
      :host(:not([${P.AUDIO}])) media-gesture-receiver[slot=gestures-chrome] {
        align-self: stretch;
        flex-grow: 1;
      }

      slot[name=middle-chrome] {
        display: inline;
        flex-grow: 1;
        pointer-events: none;
        background: none;
      }

      
      ::slotted([slot=media]),
      ::slotted([slot=poster]) {
        width: 100%;
        height: 100%;
      }

      
      :host(:not([${P.AUDIO}])) .spacer {
        flex-grow: 1;
      }

      
      :host(:-webkit-full-screen) {
        
        width: 100% !important;
        height: 100% !important;
      }

      
      ::slotted(:not([slot=media]):not([slot=poster]):not([${P.NO_AUTOHIDE}]):not([hidden]):not([role=dialog])) {
        opacity: 1;
        transition: var(--media-control-transition-in, opacity 0.25s);
      }

      
      :host([${P.USER_INACTIVE}]:not([${i.MEDIA_PAUSED}]):not([${i.MEDIA_IS_AIRPLAYING}]):not([${i.MEDIA_IS_CASTING}]):not([${P.AUDIO}])) ::slotted(:not([slot=media]):not([slot=poster]):not([${P.NO_AUTOHIDE}]):not([role=dialog])) {
        opacity: 0;
        transition: var(--media-control-transition-out, opacity 1s);
      }

      :host([${P.USER_INACTIVE}]:not([${P.NO_AUTOHIDE}]):not([${i.MEDIA_PAUSED}]):not([${i.MEDIA_IS_CASTING}]):not([${P.AUDIO}])) ::slotted([slot=media]) {
        cursor: none;
      }

      :host([${P.USER_INACTIVE}][${P.AUTOHIDE_OVER_CONTROLS}]:not([${P.NO_AUTOHIDE}]):not([${i.MEDIA_PAUSED}]):not([${i.MEDIA_IS_CASTING}]):not([${P.AUDIO}])) * {
        --media-cursor: none;
        cursor: none;
      }


      ::slotted(media-control-bar)  {
        align-self: stretch;
      }

      
      :host(:not([${P.AUDIO}])[${i.MEDIA_HAS_PLAYED}]) slot[name=poster] {
        display: none;
      }

      ::slotted([role=dialog]) {
        width: 100%;
        height: 100%;
        align-self: center;
      }

      ::slotted([role=menu]) {
        align-self: end;
      }
    </style>

    <slot name="media" part="layer media-layer"></slot>
    <slot name="poster" part="layer poster-layer"></slot>
    <slot name="gestures-chrome" part="layer gesture-layer">
      <media-gesture-receiver slot="gestures-chrome">
        <template shadowrootmode="${We.shadowRootOptions.mode}">
          ${We.getTemplateHTML({})}
        </template>
      </media-gesture-receiver>
    </slot>
    <span part="layer vertical-layer">
      <slot name="top-chrome" part="top chrome"></slot>
      <slot name="middle-chrome" part="middle chrome"></slot>
      <slot name="centered-chrome" part="layer centered-layer center centered chrome"></slot>
      
      <slot part="bottom chrome"></slot>
    </span>
    <slot name="dialog" part="layer dialog-layer"></slot>
  `}var pt=Object.values(i),mt=`sm:384 md:576 lg:768 xl:960`;function ht(e){gt(e.target,e.contentRect.width)}function gt(e,t){if(!e.isConnected)return;let n=_t(e.getAttribute(P.BREAKPOINTS)??mt),r=vt(n,t),i=!1;if(Object.keys(n).forEach(t=>{if(r.includes(t)){e.hasAttribute(`breakpoint${t}`)||(e.setAttribute(`breakpoint${t}`,``),i=!0);return}e.hasAttribute(`breakpoint${t}`)&&(e.removeAttribute(`breakpoint${t}`),i=!0)}),i){let t=new CustomEvent(a.BREAKPOINTS_CHANGE,{detail:r});e.dispatchEvent(t)}e.breakpointsComputed||(e.breakpointsComputed=!0,e.dispatchEvent(new CustomEvent(a.BREAKPOINTS_COMPUTED,{bubbles:!0,composed:!0})))}function _t(e){let t=e.split(/\s+/);return Object.fromEntries(t.map(e=>e.split(`:`)))}function vt(e,t){return Object.keys(e).filter(n=>t>=parseInt(e[n]))}var yt=class extends v.HTMLElement{constructor(){if(super(),j(this,et),j(this,nt),j(this,it),j(this,ot),j(this,ct),j(this,Ke,void 0),j(this,qe,0),j(this,Je,null),j(this,Ye,null),j(this,Xe,void 0),this.breakpointsComputed=!1,j(this,Ze,e=>{let t=this.media;for(let n of e){if(n.type!==`childList`)continue;let e=n.removedNodes;for(let r of e){if(r.slot!=`media`||n.target!=this)continue;let e=n.previousSibling&&n.previousSibling.previousElementSibling;if(!e||!t)this.mediaUnsetCallback(r);else{let t=e.slot!==`media`;for(;(e=e.previousSibling)!==null;)e.slot==`media`&&(t=!1);t&&this.mediaUnsetCallback(r)}}if(t)for(let e of n.addedNodes)e===t&&this.handleMediaUpdated(t)}}),j(this,Qe,!1),j(this,$e,e=>{A(this,Qe)||(setTimeout(()=>{ht(e),M(this,Qe,!1)},0),M(this,Qe,!0))}),j(this,ut,void 0),j(this,dt,()=>{if(!A(this,ut).assignedElements({flatten:!0}).length){A(this,Je)&&this.mediaUnsetCallback(A(this,Je));return}this.handleMediaUpdated(this.media)}),!this.shadowRoot){this.attachShadow(this.constructor.shadowRootOptions);let e=b(this.attributes),t=this.constructor.getTemplateHTML(e);this.shadowRoot.setHTMLUnsafe?this.shadowRoot.setHTMLUnsafe(t):this.shadowRoot.innerHTML=t}M(this,Ke,new MutationObserver(A(this,Ze)))}static get observedAttributes(){return[P.AUTOHIDE,P.GESTURES_DISABLED].concat(pt).filter(e=>![i.MEDIA_RENDITION_LIST,i.MEDIA_AUDIO_TRACK_LIST,i.MEDIA_CHAPTERS_CUES,i.MEDIA_WIDTH,i.MEDIA_HEIGHT,i.MEDIA_ERROR,i.MEDIA_ERROR_MESSAGE].includes(e))}attributeChangedCallback(e,t,n){e.toLowerCase()==P.AUTOHIDE&&(this.autohide=n)}get media(){let e=this.querySelector(`:scope > [slot=media]`);return e?.nodeName==`SLOT`&&(e=e.assignedElements({flatten:!0})[0]),e}async handleMediaUpdated(e){e&&(M(this,Je,e),e.localName.includes(`-`)&&await v.customElements.whenDefined(e.localName),this.mediaSetCallback(e))}connectedCallback(){var e;A(this,Ke).observe(this,{childList:!0,subtree:!0}),Ce(this,A(this,$e));let t=this.getAttribute(P.AUDIO)==null?_(`video player`):_(`audio player`);this.setAttribute(`role`,`region`),this.setAttribute(`aria-label`,t),this.handleMediaUpdated(this.media),this.setAttribute(P.USER_INACTIVE,``),gt(this,this.getBoundingClientRect().width);let n=this.querySelector(`:scope > slot[slot=media]`);n&&(M(this,ut,n),A(this,ut).addEventListener(`slotchange`,A(this,dt))),this.addEventListener(`pointerdown`,this),this.addEventListener(`pointermove`,this),this.addEventListener(`pointerup`,this),this.addEventListener(`mouseleave`,this),this.addEventListener(`keyup`,this),(e=v.window)==null||e.addEventListener(`mouseup`,this)}disconnectedCallback(){var e;we(this,A(this,$e)),clearTimeout(A(this,Ye)),A(this,Ke).disconnect(),this.media&&this.mediaUnsetCallback(this.media),(e=v.window)==null||e.removeEventListener(`mouseup`,this),this.removeEventListener(`pointerdown`,this),this.removeEventListener(`pointermove`,this),this.removeEventListener(`pointerup`,this),this.removeEventListener(`mouseleave`,this),this.removeEventListener(`keyup`,this),A(this,ut)&&(A(this,ut).removeEventListener(`slotchange`,A(this,dt)),M(this,ut,null)),M(this,Qe,!1)}mediaSetCallback(e){}mediaUnsetCallback(e){M(this,Je,null)}handleEvent(e){switch(e.type){case`pointerdown`:M(this,qe,e.timeStamp);break;case`pointermove`:N(this,et,tt).call(this,e);break;case`pointerup`:N(this,nt,rt).call(this,e);break;case`mouseleave`:N(this,it,at).call(this);break;case`mouseup`:this.removeAttribute(P.KEYBOARD_CONTROL);break;case`keyup`:N(this,ct,lt).call(this),this.setAttribute(P.KEYBOARD_CONTROL,``)}}set autohide(e){let t=Number(e);M(this,Xe,isNaN(t)?0:t)}get autohide(){return(A(this,Xe)===void 0?2:A(this,Xe)).toString()}get breakpoints(){return E(this,P.BREAKPOINTS)}set breakpoints(e){D(this,P.BREAKPOINTS,e)}get audio(){return w(this,P.AUDIO)}set audio(e){T(this,P.AUDIO,e)}get gesturesDisabled(){return w(this,P.GESTURES_DISABLED)}set gesturesDisabled(e){T(this,P.GESTURES_DISABLED,e)}get keyboardControl(){return w(this,P.KEYBOARD_CONTROL)}set keyboardControl(e){T(this,P.KEYBOARD_CONTROL,e)}get noAutohide(){return w(this,P.NO_AUTOHIDE)}set noAutohide(e){T(this,P.NO_AUTOHIDE,e)}get autohideOverControls(){return w(this,P.AUTOHIDE_OVER_CONTROLS)}set autohideOverControls(e){T(this,P.AUTOHIDE_OVER_CONTROLS,e)}get userInteractive(){return w(this,P.USER_INACTIVE)}set userInteractive(e){T(this,P.USER_INACTIVE,e)}};Ke=new WeakMap,qe=new WeakMap,Je=new WeakMap,Ye=new WeakMap,Xe=new WeakMap,Ze=new WeakMap,Qe=new WeakMap,$e=new WeakMap,et=new WeakSet,tt=function(e){if(e.pointerType!==`mouse`&&e.timeStamp-A(this,qe)<250)return;N(this,ot,st).call(this),clearTimeout(A(this,Ye));let t=this.hasAttribute(P.AUTOHIDE_OVER_CONTROLS);([this,this.media].includes(e.target)||t)&&N(this,ct,lt).call(this)},nt=new WeakSet,rt=function(e){if(e.pointerType===`touch`){let t=!this.hasAttribute(P.USER_INACTIVE);[this,this.media].includes(e.target)&&t?N(this,it,at).call(this):N(this,ct,lt).call(this)}else e.composedPath().some(e=>[`media-play-button`,`media-fullscreen-button`].includes(e?.localName))&&N(this,ct,lt).call(this)},it=new WeakSet,at=function(){if(A(this,Xe)<0||this.hasAttribute(P.USER_INACTIVE))return;this.setAttribute(P.USER_INACTIVE,``);let e=new v.CustomEvent(a.USER_INACTIVE_CHANGE,{composed:!0,bubbles:!0,detail:!0});this.dispatchEvent(e)},ot=new WeakSet,st=function(){if(!this.hasAttribute(P.USER_INACTIVE))return;this.removeAttribute(P.USER_INACTIVE);let e=new v.CustomEvent(a.USER_INACTIVE_CHANGE,{composed:!0,bubbles:!0,detail:!1});this.dispatchEvent(e)},ct=new WeakSet,lt=function(){N(this,ot,st).call(this),clearTimeout(A(this,Ye));let e=parseInt(this.autohide);e<0||M(this,Ye,setTimeout(()=>{N(this,it,at).call(this)},e*1e3))},ut=new WeakMap,dt=new WeakMap,yt.shadowRootOptions={mode:`open`},yt.getTemplateHTML=ft,v.customElements.get(`media-container`)||v.customElements.define(`media-container`,yt);var bt=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},F=(e,t,n)=>(bt(e,t,`read from private field`),n?n.call(e):t.get(e)),xt=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},St=(e,t,n,r)=>(bt(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),Ct,wt,Tt,Et,Dt,Ot,kt=class{constructor(e,t,{defaultValue:n}={defaultValue:void 0}){xt(this,Dt),xt(this,Ct,void 0),xt(this,wt,void 0),xt(this,Tt,void 0),xt(this,Et,new Set),St(this,Ct,e),St(this,wt,t),St(this,Tt,new Set(n))}[Symbol.iterator](){return F(this,Dt,Ot).values()}get length(){return F(this,Dt,Ot).size}get value(){return[...F(this,Dt,Ot)].join(` `)??``}set value(e){e!==this.value&&(St(this,Et,new Set),this.add(...e?.split(` `)??[]))}toString(){return this.value}item(e){return[...F(this,Dt,Ot)][e]}values(){return F(this,Dt,Ot).values()}forEach(e,t){F(this,Dt,Ot).forEach(e,t)}add(...e){var t;e.forEach(e=>F(this,Et).add(e)),!(this.value===``&&!F(this,Ct)?.hasAttribute(`${F(this,wt)}`))&&((t=F(this,Ct))==null||t.setAttribute(`${F(this,wt)}`,`${this.value}`))}remove(...e){var t;e.forEach(e=>F(this,Et).delete(e)),(t=F(this,Ct))==null||t.setAttribute(`${F(this,wt)}`,`${this.value}`)}contains(e){return F(this,Dt,Ot).has(e)}toggle(e,t){return t===void 0?this.contains(e)?(this.remove(e),!1):(this.add(e),!0):t?(this.add(e),!0):(this.remove(e),!1)}replace(e,t){return this.remove(e),this.add(t),e===t}};Ct=new WeakMap,wt=new WeakMap,Tt=new WeakMap,Et=new WeakMap,Dt=new WeakSet,Ot=function(){return F(this,Et).size?F(this,Et):F(this,Tt)};var At=(e=``)=>e.split(/\s+/),jt=(e=``)=>{let[t,n,r]=e.split(`:`),i=r?decodeURIComponent(r):void 0;return{kind:t===`cc`?s.CAPTIONS:s.SUBTITLES,language:n,label:i}},Mt=(e=``,t={})=>At(e).map(e=>{let n=jt(e);return{...t,...n}}),Nt=e=>e?Array.isArray(e)?e.map(e=>typeof e==`string`?jt(e):e):typeof e==`string`?Mt(e):[e]:[],Pt=({kind:e,label:t,language:n}={kind:`subtitles`})=>t?`${e===`captions`?`cc`:`sb`}:${n}:${encodeURIComponent(t)}`:n,Ft=(e=[])=>Array.prototype.map.call(e,Pt).join(` `),It=(e,t)=>n=>n[e]===t,Lt=e=>{let t=Object.entries(e).map(([e,t])=>It(e,t));return e=>t.every(t=>t(e))},Rt=(e,t=[],n=[])=>{let r=Nt(n).map(Lt);Array.from(t).filter(e=>r.some(t=>t(e))).forEach(t=>{t.mode=e})},zt=(e,t=()=>!0)=>{if(!e?.textTracks)return[];let n=typeof t==`function`?t:Lt(t);return Array.from(e.textTracks).filter(n)},Bt=e=>!!e.mediaSubtitlesShowing?.length||e.hasAttribute(i.MEDIA_SUBTITLES_SHOWING),Vt=e=>{let{media:t,fullscreenElement:n}=e;try{let e=n&&`requestFullscreen`in n?`requestFullscreen`:n&&`webkitRequestFullScreen`in n?`webkitRequestFullScreen`:void 0;if(e){let t=n[e]?.call(n);if(t instanceof Promise)return t.catch(()=>{})}else t?.webkitEnterFullscreen?t.webkitEnterFullscreen():t?.requestFullscreen&&t.requestFullscreen()}catch(e){console.error(e)}},Ht=`exitFullscreen`in y?`exitFullscreen`:`webkitExitFullscreen`in y?`webkitExitFullscreen`:`webkitCancelFullScreen`in y?`webkitCancelFullScreen`:void 0,Ut=e=>{let{documentElement:t}=e;if(Ht){let e=(t?.[Ht])?.call(t);if(e instanceof Promise)return e.catch(()=>{})}},Wt=`fullscreenElement`in y?`fullscreenElement`:`webkitFullscreenElement`in y?`webkitFullscreenElement`:void 0,Gt=e=>{let{documentElement:t,media:n}=e,r=t?.[Wt];return!r&&`webkitDisplayingFullscreen`in n&&`webkitPresentationMode`in n&&n.webkitDisplayingFullscreen&&n.webkitPresentationMode===f.FULLSCREEN?n:r},Kt=e=>{let{media:t,documentElement:n,fullscreenElement:r=t}=e;if(!t||!n)return!1;let i=Gt(e);if(!i)return!1;if(i===r||i===t)return!0;if(i.localName.includes(`-`)){let e=i.shadowRoot;if(!(Wt in e))return Ae(i,r);for(;e?.[Wt];){if(e[Wt]===r)return!0;e=e[Wt]?.shadowRoot}}return!1},qt=`fullscreenEnabled`in y?`fullscreenEnabled`:`webkitFullscreenEnabled`in y?`webkitFullscreenEnabled`:void 0,Jt=e=>{let{documentElement:t,media:n}=e;return!!t?.[qt]||n&&`webkitSupportsFullscreen`in n},Yt,Xt=()=>{var e;return Yt||(Yt=((e=y)?.createElement)?.call(e,`video`),Yt)},Zt=async(e=Xt())=>{if(!e)return!1;let t=e.volume;e.volume=t/2+.1;let n=new AbortController,r=await Promise.race([Qt(e,n.signal),$t(e,t)]);return n.abort(),r},Qt=(e,t)=>new Promise(n=>{e.addEventListener(`volumechange`,()=>n(!0),{signal:t})}),$t=async(e,t)=>{for(let n=0;n<10;n++){if(e.volume===t)return!1;await ne(10)}return e.volume!==t},en=/.*Version\/.*Safari\/.*/.test(v.navigator.userAgent),tn=(e=Xt())=>v.matchMedia(`(display-mode: standalone)`).matches&&en?!1:typeof e?.requestPictureInPicture==`function`,nn=(e=Xt())=>Jt({documentElement:y,media:e}),rn=nn(),an=tn(),on=!!v.WebKitPlaybackTargetAvailabilityEvent,sn=!!v.chrome,cn=e=>zt(e.media,e=>[s.SUBTITLES,s.CAPTIONS].includes(e.kind)).sort((e,t)=>e.kind>=t.kind?1:-1),ln=e=>zt(e.media,e=>e.mode===c.SHOWING&&[s.SUBTITLES,s.CAPTIONS].includes(e.kind)),un=(e,t)=>{let n=cn(e),r=ln(e),i=!!r.length;if(n.length){if(t===!1||i&&t!==!0)Rt(c.DISABLED,n,r);else if(t===!0||!i&&t!==!1){let t=n[0],{options:i}=e;if(!i?.noSubtitlesLangPref){let e=v.localStorage.getItem(`media-chrome-pref-subtitles-lang`),r=e?[e,...v.navigator.languages]:v.navigator.languages,i=n.filter(e=>r.some(t=>e.language.toLowerCase().startsWith(t.split(`-`)[0]))).sort((e,t)=>r.findIndex(t=>e.language.toLowerCase().startsWith(t.split(`-`)[0]))-r.findIndex(e=>t.language.toLowerCase().startsWith(e.split(`-`)[0])));i[0]&&(t=i[0])}let{language:a,label:o,kind:s}=t;Rt(c.DISABLED,n,r),Rt(c.SHOWING,n,[{language:a,label:o,kind:s}])}}},dn=(e,t)=>e===t?!0:e==null||t==null||typeof e!=typeof t?!1:typeof e==`number`&&Number.isNaN(e)&&Number.isNaN(t)?!0:typeof e==`object`?Array.isArray(e)?fn(e,t):Object.entries(e).every(([e,n])=>e in t&&dn(n,t[e])):!1,fn=(e,t)=>{let n=Array.isArray(e),r=Array.isArray(t);return n===r?n||r?e.length===t.length&&e.every((e,n)=>dn(e,t[n])):!0:!1},pn=Object.values(d),mn,hn=Zt().then(e=>(mn=e,mn)),gn=async(...e)=>{await Promise.all(e.filter(e=>e).map(async e=>{if(!(`localName`in e&&e instanceof v.HTMLElement))return;let t=e.localName;if(!t.includes(`-`))return;let n=v.customElements.get(t);n&&e instanceof n||(await v.customElements.whenDefined(t),v.customElements.upgrade(e))}))},_n=new v.DOMParser,vn=e=>e&&(_n.parseFromString(e,`text/html`).body.textContent||e),yn={mediaError:{get(e,t){let{media:n}=e;if(t?.type!==`playing`)return n?.error},mediaEvents:[`emptied`,`error`,`playing`]},mediaErrorCode:{get(e,t){let{media:n}=e;if(t?.type!==`playing`)return n?.error?.code},mediaEvents:[`emptied`,`error`,`playing`]},mediaErrorMessage:{get(e,t){let{media:n}=e;if(t?.type!==`playing`)return n?.error?.message??``},mediaEvents:[`emptied`,`error`,`playing`]},mediaWidth:{get(e){let{media:t}=e;return t?.videoWidth??0},mediaEvents:[`resize`]},mediaHeight:{get(e){let{media:t}=e;return t?.videoHeight??0},mediaEvents:[`resize`]},mediaPaused:{get(e){let{media:t}=e;return t?.paused??!0},set(e,t){var n;let{media:r}=t;r&&(e?r.pause():(n=r.play())==null||n.catch(()=>{}))},mediaEvents:[`play`,`playing`,`pause`,`emptied`]},mediaHasPlayed:{get(e,t){let{media:n}=e;return n?t?t.type===`playing`:!n.paused:!1},mediaEvents:[`playing`,`emptied`]},mediaEnded:{get(e){let{media:t}=e;return t?.ended??!1},mediaEvents:[`seeked`,`ended`,`emptied`]},mediaPlaybackRate:{get(e){let{media:t}=e;return t?.playbackRate??1},set(e,t){let{media:n}=t;n&&Number.isFinite(+e)&&(n.playbackRate=+e)},mediaEvents:[`ratechange`,`loadstart`]},mediaMuted:{get(e){let{media:t}=e;return t?.muted??!1},set(e,t){let{media:n,options:{noMutedPref:r}={}}=t;if(n){n.muted=e;try{let t=v.localStorage.getItem(`media-chrome-pref-muted`)!==null,i=n.hasAttribute(`muted`);if(r){t&&v.localStorage.removeItem(`media-chrome-pref-muted`);return}if(i&&!t)return;v.localStorage.setItem(`media-chrome-pref-muted`,e?`true`:`false`)}catch(e){console.debug(`Error setting muted pref`,e)}}},mediaEvents:[`volumechange`],stateOwnersUpdateHandlers:[(e,t)=>{let{options:{noMutedPref:n}}=t,{media:r}=t;if(!(!r||r.muted||n))try{let n=v.localStorage.getItem(`media-chrome-pref-muted`)===`true`;yn.mediaMuted.set(n,t),e(n)}catch(e){console.debug(`Error getting muted pref`,e)}}]},mediaLoop:{get(e){let{media:t}=e;return t?.loop},set(e,t){let{media:n}=t;n&&(n.loop=e)},mediaEvents:[`medialooprequest`]},mediaVolume:{get(e){let{media:t}=e;return t?.volume??1},set(e,t){let{media:n,options:{noVolumePref:r}={}}=t;if(n){try{e==null?v.localStorage.removeItem(`media-chrome-pref-volume`):!n.hasAttribute(`muted`)&&!r&&v.localStorage.setItem(`media-chrome-pref-volume`,e.toString())}catch(e){console.debug(`Error setting volume pref`,e)}Number.isFinite(+e)&&(n.volume=+e)}},mediaEvents:[`volumechange`],stateOwnersUpdateHandlers:[(e,t)=>{let{options:{noVolumePref:n}}=t;if(!n)try{let{media:n}=t;if(!n)return;let r=v.localStorage.getItem(`media-chrome-pref-volume`);if(r==null)return;yn.mediaVolume.set(+r,t),e(+r)}catch(e){console.debug(`Error getting volume pref`,e)}}]},mediaVolumeLevel:{get(e){let{media:t}=e;return t?.volume===void 0?`high`:t.muted||t.volume===0?`off`:t.volume<.5?`low`:t.volume<.75?`medium`:`high`},mediaEvents:[`volumechange`]},mediaCurrentTime:{get(e){let{media:t}=e;return t?.currentTime??0},set(e,t){let{media:n}=t;!n||!te(e)||(n.currentTime=e)},mediaEvents:[`timeupdate`,`loadedmetadata`]},mediaDuration:{get(e){let{media:t,options:{defaultDuration:n}={}}=e;return n&&(!t||!t.duration||Number.isNaN(t.duration)||!Number.isFinite(t.duration))?n:Number.isFinite(t?.duration)?t.duration:NaN},mediaEvents:[`durationchange`,`loadedmetadata`,`emptied`]},mediaLoading:{get(e){let{media:t}=e;return t?.readyState<3},mediaEvents:[`waiting`,`playing`,`emptied`]},mediaSeekable:{get(e){let{media:t}=e;if(!t?.seekable?.length)return;let n=t.seekable.start(0),r=t.seekable.end(t.seekable.length-1);if(!(!n&&!r))return[Number(n.toFixed(3)),Number(r.toFixed(3))]},mediaEvents:[`loadedmetadata`,`emptied`,`progress`,`seekablechange`]},mediaBuffered:{get(e){let{media:t}=e,n=t?.buffered??[];return Array.from(n).map((e,t)=>[Number(n.start(t).toFixed(3)),Number(n.end(t).toFixed(3))])},mediaEvents:[`progress`,`emptied`]},mediaStreamType:{get(e){let{media:t,options:{defaultStreamType:n}={}}=e,r=[d.LIVE,d.ON_DEMAND].includes(n)?n:void 0;if(!t)return r;let{streamType:i}=t;if(pn.includes(i))return i===d.UNKNOWN?r:i;let a=t.duration;return a===1/0?d.LIVE:Number.isFinite(a)?d.ON_DEMAND:r},mediaEvents:[`emptied`,`durationchange`,`loadedmetadata`,`streamtypechange`]},mediaTargetLiveWindow:{get(e){let{media:t}=e;if(!t)return NaN;let{targetLiveWindow:n}=t,r=yn.mediaStreamType.get(e);return(n==null||Number.isNaN(n))&&r===d.LIVE?0:n},mediaEvents:[`emptied`,`durationchange`,`loadedmetadata`,`streamtypechange`,`targetlivewindowchange`]},mediaTimeIsLive:{get(e){let{media:t,options:{liveEdgeOffset:n=10}={}}=e;if(!t)return!1;if(typeof t.liveEdgeStart==`number`)return!Number.isNaN(t.liveEdgeStart)&&t.currentTime>=t.liveEdgeStart;if(yn.mediaStreamType.get(e)!==d.LIVE)return!1;let r=t.seekable;if(!r)return!0;if(!r.length)return!1;let i=r.end(r.length-1)-n;return t.currentTime>=i},mediaEvents:[`playing`,`timeupdate`,`progress`,`waiting`,`emptied`]},mediaSubtitlesList:{get(e){return cn(e).map(({kind:e,label:t,language:n})=>({kind:e,label:t,language:n}))},mediaEvents:[`loadstart`],textTracksEvents:[`addtrack`,`removetrack`]},mediaSubtitlesShowing:{get(e){return ln(e).map(({kind:e,label:t,language:n})=>({kind:e,label:t,language:n}))},mediaEvents:[`loadstart`],textTracksEvents:[`addtrack`,`removetrack`,`change`],stateOwnersUpdateHandlers:[(e,t)=>{var n,r;let{media:i,options:a}=t;if(!i)return;let o=e=>{a.defaultSubtitles&&(e&&![s.CAPTIONS,s.SUBTITLES].includes(e?.track?.kind)||un(t,!0))};return i.addEventListener(`loadstart`,o),(n=i.textTracks)==null||n.addEventListener(`addtrack`,o),(r=i.textTracks)==null||r.addEventListener(`removetrack`,o),()=>{var e,t;i.removeEventListener(`loadstart`,o),(e=i.textTracks)==null||e.removeEventListener(`addtrack`,o),(t=i.textTracks)==null||t.removeEventListener(`removetrack`,o)}}]},mediaChaptersCues:{get(e){let{media:t}=e;if(!t)return[];let[n]=zt(t,{kind:s.CHAPTERS});return Array.from(n?.cues??[]).map(({text:e,startTime:t,endTime:n})=>({text:vn(e),startTime:t,endTime:n}))},mediaEvents:[`loadstart`,`loadedmetadata`],textTracksEvents:[`addtrack`,`removetrack`,`change`],stateOwnersUpdateHandlers:[(e,t)=>{let{media:n}=t;if(!n)return;let r=n.querySelector(`track[kind="chapters"][default][src]`),i=n.shadowRoot?.querySelector(`:is(video,audio) > track[kind="chapters"][default][src]`);return r?.addEventListener(`load`,e),i?.addEventListener(`load`,e),()=>{r?.removeEventListener(`load`,e),i?.removeEventListener(`load`,e)}}]},mediaIsPip:{get(e){let{media:t,documentElement:n}=e;if(!t||!n||!n.pictureInPictureElement)return!1;if(n.pictureInPictureElement===t)return!0;if(n.pictureInPictureElement instanceof HTMLMediaElement)return t.localName?.includes(`-`)?Ae(t,n.pictureInPictureElement):!1;if(n.pictureInPictureElement.localName.includes(`-`)){let e=n.pictureInPictureElement.shadowRoot;for(;e?.pictureInPictureElement;){if(e.pictureInPictureElement===t)return!0;e=e.pictureInPictureElement?.shadowRoot}}return!1},set(e,t){let{media:n}=t;if(n){if(e){if(!y.pictureInPictureEnabled){console.warn(`MediaChrome: Picture-in-picture is not enabled`);return}if(!n.requestPictureInPicture){console.warn(`MediaChrome: The current media does not support picture-in-picture`);return}let e=()=>{console.warn(`MediaChrome: The media is not ready for picture-in-picture. It must have a readyState > 0.`)};n.requestPictureInPicture().catch(t=>{if(t.code===11){if(!n.src){console.warn(`MediaChrome: The media is not ready for picture-in-picture. It must have a src set.`);return}if(n.readyState===0&&n.preload===`none`){let t=()=>{n.removeEventListener(`loadedmetadata`,r),n.preload=`none`},r=()=>{n.requestPictureInPicture().catch(e),t()};n.addEventListener(`loadedmetadata`,r),n.preload=`metadata`,setTimeout(()=>{n.readyState===0&&e(),t()},1e3)}else throw t}else throw t})}else y.pictureInPictureElement&&y.exitPictureInPicture()}},mediaEvents:[`enterpictureinpicture`,`leavepictureinpicture`]},mediaRenditionList:{get(e){let{media:t}=e;return[...t?.videoRenditions??[]].map(e=>({...e}))},mediaEvents:[`emptied`,`loadstart`],videoRenditionsEvents:[`addrendition`,`removerendition`]},mediaRenditionSelected:{get(e){let{media:t}=e;return t?.videoRenditions?.[t.videoRenditions?.selectedIndex]?.id},set(e,t){let{media:n}=t;if(!n?.videoRenditions){console.warn(`MediaController: Rendition selection not supported by this media.`);return}let r=e,i=Array.prototype.findIndex.call(n.videoRenditions,e=>e.id==r);n.videoRenditions.selectedIndex!=i&&(n.videoRenditions.selectedIndex=i)},mediaEvents:[`emptied`],videoRenditionsEvents:[`addrendition`,`removerendition`,`change`]},mediaAudioTrackList:{get(e){let{media:t}=e;return[...t?.audioTracks??[]]},mediaEvents:[`emptied`,`loadstart`],audioTracksEvents:[`addtrack`,`removetrack`]},mediaAudioTrackEnabled:{get(e){let{media:t}=e;return[...t?.audioTracks??[]].find(e=>e.enabled)?.id},set(e,t){let{media:n}=t;if(!n?.audioTracks){console.warn(`MediaChrome: Audio track selection not supported by this media.`);return}let r=e;for(let e of n.audioTracks)e.enabled=r==e.id},mediaEvents:[`emptied`],audioTracksEvents:[`addtrack`,`removetrack`,`change`]},mediaIsFullscreen:{get(e){return Kt(e)},set(e,t,n){var r;e?(Vt(t),n.detail&&!t.media?.inert&&((r=t.media)==null||r.focus())):Ut(t)},rootEvents:[`fullscreenchange`,`webkitfullscreenchange`],mediaEvents:[`webkitbeginfullscreen`,`webkitendfullscreen`,`webkitpresentationmodechanged`]},mediaIsCasting:{get(e){let{media:t}=e;return!t?.remote||t.remote?.state===`disconnected`?!1:t.remote.state===`connected`},set(e,t){let{media:n}=t;if(n&&!(e&&n.remote?.state!==`disconnected`)&&!(!e&&n.remote?.state!==`connected`)){if(typeof n.remote.prompt!=`function`){console.warn(`MediaChrome: Casting is not supported in this environment`);return}n.remote.prompt().catch(()=>{})}},remoteEvents:[`connect`,`connecting`,`disconnect`]},mediaIsAirplaying:{get(){return!1},set(e,t){let{media:n}=t;if(n){if(!(n.webkitShowPlaybackTargetPicker&&v.WebKitPlaybackTargetAvailabilityEvent)){console.error(`MediaChrome: received a request to select AirPlay but AirPlay is not supported in this environment`);return}n.webkitShowPlaybackTargetPicker()}},mediaEvents:[`webkitcurrentplaybacktargetiswirelesschanged`]},mediaFullscreenUnavailable:{get(e){let{media:t}=e;if(!rn||!nn(t))return u.UNSUPPORTED}},mediaPipUnavailable:{get(e){let{media:t}=e;if(!an||!tn(t))return u.UNSUPPORTED;if(t?.disablePictureInPicture)return u.UNAVAILABLE}},mediaVolumeUnavailable:{get(e){let{media:t}=e;if(mn===!1||t?.volume==null)return u.UNSUPPORTED},stateOwnersUpdateHandlers:[e=>{mn??hn.then(t=>e(t?void 0:u.UNSUPPORTED))}]},mediaCastUnavailable:{get(e,{availability:t=`not-available`}={}){let{media:n}=e;if(!sn||!n?.remote?.state)return u.UNSUPPORTED;if(t!=null&&t!==`available`)return u.UNAVAILABLE},stateOwnersUpdateHandlers:[(e,t)=>{var n;let{media:r}=t;if(r)return r.disableRemotePlayback||r.hasAttribute(`disableremoteplayback`)||(n=r?.remote)==null||n.watchAvailability(t=>{e({availability:t?`available`:`not-available`})}).catch(t=>{t.name===`NotSupportedError`?e({availability:null}):e({availability:`not-available`})}),()=>{var e;(e=r?.remote)==null||e.cancelWatchAvailability().catch(()=>{})}}]},mediaAirplayUnavailable:{get(e,t){if(!on)return u.UNSUPPORTED;if(t?.availability===`not-available`)return u.UNAVAILABLE},mediaEvents:[`webkitplaybacktargetavailabilitychanged`],stateOwnersUpdateHandlers:[(e,t)=>{var n;let{media:r}=t;if(r)return r.disableRemotePlayback||r.hasAttribute(`disableremoteplayback`)||(n=r?.remote)==null||n.watchAvailability(t=>{e({availability:t?`available`:`not-available`})}).catch(t=>{t.name===`NotSupportedError`?e({availability:null}):e({availability:`not-available`})}),()=>{var e;(e=r?.remote)==null||e.cancelWatchAvailability().catch(()=>{})}}]},mediaRenditionUnavailable:{get(e){let{media:t}=e;if(!t?.videoRenditions)return u.UNSUPPORTED;if(!t.videoRenditions?.length)return u.UNAVAILABLE},mediaEvents:[`emptied`,`loadstart`],videoRenditionsEvents:[`addrendition`,`removerendition`]},mediaAudioTrackUnavailable:{get(e){let{media:t}=e;if(!t?.audioTracks)return u.UNSUPPORTED;if((t.audioTracks?.length??0)<=1)return u.UNAVAILABLE},mediaEvents:[`emptied`,`loadstart`],audioTracksEvents:[`addtrack`,`removetrack`]},mediaLang:{get(e){let{options:{mediaLang:t}={}}=e;return t??`en`}}},bn={[e.MEDIA_PREVIEW_REQUEST](e,t,{detail:n}){let{media:r}=t,i=n??void 0,a,o;if(r&&i!=null){let[e]=zt(r,{kind:s.METADATA,label:`thumbnails`}),t=Array.prototype.find.call(e?.cues??[],(e,t,n)=>t===0?e.endTime>i:t===n.length-1?e.startTime<=i:e.startTime<=i&&e.endTime>i);if(t){let e=/'^(?:[a-z]+:)?\/\//i.test(t.text)?void 0:r?.querySelector(`track[label="thumbnails"]`)?.src,n=new URL(t.text,e);o=new URLSearchParams(n.hash).get(`#xywh`).split(`,`).map(e=>+e),a=n.href}}let c=e.mediaDuration.get(t),l=e.mediaChaptersCues.get(t).find((e,t,n)=>t===n.length-1&&c===e.endTime?e.startTime<=i&&e.endTime>=i:e.startTime<=i&&e.endTime>i)?.text;return n!=null&&l==null&&(l=``),{mediaPreviewTime:i,mediaPreviewImage:a,mediaPreviewCoords:o,mediaPreviewChapter:l}},[e.MEDIA_PAUSE_REQUEST](e,t){e.mediaPaused.set(!0,t)},[e.MEDIA_PLAY_REQUEST](e,t){let n=e.mediaStreamType.get(t)===d.LIVE,r=!t.options?.noAutoSeekToLive,i=e.mediaTargetLiveWindow.get(t)>0;if(n&&r&&!i){let n=e.mediaSeekable.get(t)?.[1];if(n){let r=n-(t.options?.seekToLiveOffset??0);e.mediaCurrentTime.set(r,t)}}e.mediaPaused.set(!1,t)},[e.MEDIA_PLAYBACK_RATE_REQUEST](e,t,{detail:n}){let r=n;e.mediaPlaybackRate.set(r,t)},[e.MEDIA_MUTE_REQUEST](e,t){e.mediaMuted.set(!0,t)},[e.MEDIA_UNMUTE_REQUEST](e,t){e.mediaVolume.get(t)||e.mediaVolume.set(.25,t),e.mediaMuted.set(!1,t)},[e.MEDIA_LOOP_REQUEST](e,t,{detail:n}){let r=!!n;return e.mediaLoop.set(r,t),{mediaLoop:r}},[e.MEDIA_VOLUME_REQUEST](e,t,{detail:n}){let r=n;r&&e.mediaMuted.get(t)&&e.mediaMuted.set(!1,t),e.mediaVolume.set(r,t)},[e.MEDIA_SEEK_REQUEST](e,t,{detail:n}){let r=n;e.mediaCurrentTime.set(r,t)},[e.MEDIA_SEEK_TO_LIVE_REQUEST](e,t){let n=e.mediaSeekable.get(t)?.[1];if(Number.isNaN(Number(n)))return;let r=n-(t.options?.seekToLiveOffset??0);e.mediaCurrentTime.set(r,t)},[e.MEDIA_SHOW_SUBTITLES_REQUEST](e,t,{detail:n}){let{options:r}=t,i=cn(t),a=Nt(n),o=a[0]?.language;o&&!r.noSubtitlesLangPref&&v.localStorage.setItem(`media-chrome-pref-subtitles-lang`,o),Rt(c.SHOWING,i,a)},[e.MEDIA_DISABLE_SUBTITLES_REQUEST](e,t,{detail:n}){let r=cn(t),i=n??[];Rt(c.DISABLED,r,i)},[e.MEDIA_TOGGLE_SUBTITLES_REQUEST](e,t,{detail:n}){un(t,n)},[e.MEDIA_RENDITION_REQUEST](e,t,{detail:n}){let r=n;e.mediaRenditionSelected.set(r,t)},[e.MEDIA_AUDIO_TRACK_REQUEST](e,t,{detail:n}){let r=n;e.mediaAudioTrackEnabled.set(r,t)},[e.MEDIA_ENTER_PIP_REQUEST](e,t){e.mediaIsFullscreen.get(t)&&e.mediaIsFullscreen.set(!1,t),e.mediaIsPip.set(!0,t)},[e.MEDIA_EXIT_PIP_REQUEST](e,t){e.mediaIsPip.set(!1,t)},[e.MEDIA_ENTER_FULLSCREEN_REQUEST](e,t,n){e.mediaIsPip.get(t)&&e.mediaIsPip.set(!1,t),e.mediaIsFullscreen.set(!0,t,n)},[e.MEDIA_EXIT_FULLSCREEN_REQUEST](e,t){e.mediaIsFullscreen.set(!1,t)},[e.MEDIA_ENTER_CAST_REQUEST](e,t){e.mediaIsFullscreen.get(t)&&e.mediaIsFullscreen.set(!1,t),e.mediaIsCasting.set(!0,t)},[e.MEDIA_EXIT_CAST_REQUEST](e,t){e.mediaIsCasting.set(!1,t)},[e.MEDIA_AIRPLAY_REQUEST](e,t){e.mediaIsAirplaying.set(!0,t)}},xn=({media:e,fullscreenElement:t,documentElement:n,stateMediator:r=yn,requestMap:i=bn,options:a={},monitorStateOwnersOnlyWithSubscriptions:o=!0})=>{let s=[],c={options:{...a}},l=Object.freeze({mediaPreviewTime:void 0,mediaPreviewImage:void 0,mediaPreviewCoords:void 0,mediaPreviewChapter:void 0}),u=e=>{e!=null&&(dn(e,l)||(l=Object.freeze({...l,...e}),s.forEach(e=>e(l))))},d=()=>{let e=Object.entries(r).reduce((e,[t,{get:n}])=>(e[t]=n(c),e),{});u(e)},f={},p,m=async(e,t)=>{let n=!!p;if(p={...c,...p??{},...e},n)return;await gn(...Object.values(e));let i=s.length>0&&t===0&&o,a=c.media!==p.media,l=c.media?.textTracks!==p.media?.textTracks,m=c.media?.videoRenditions!==p.media?.videoRenditions,h=c.media?.audioTracks!==p.media?.audioTracks,ee=c.media?.remote!==p.media?.remote,te=c.documentElement!==p.documentElement,ne=!!c.media&&(a||i),g=!!c.media?.textTracks&&(l||i),re=!!c.media?.videoRenditions&&(m||i),ie=!!c.media?.audioTracks&&(h||i),ae=!!c.media?.remote&&(ee||i),oe=!!c.documentElement&&(te||i),se=ne||g||re||ie||ae||oe,_=s.length===0&&t===1&&o,ce=!!p.media&&(a||_),le=!!p.media?.textTracks&&(l||_),ue=!!p.media?.videoRenditions&&(m||_),de=!!p.media?.audioTracks&&(h||_),fe=!!p.media?.remote&&(ee||_),pe=!!p.documentElement&&(te||_),me=ce||le||ue||de||fe||pe;if(!(se||me)){Object.entries(p).forEach(([e,t])=>{c[e]=t}),d(),p=void 0;return}Object.entries(r).forEach(([e,{get:t,mediaEvents:n=[],textTracksEvents:r=[],videoRenditionsEvents:i=[],audioTracksEvents:a=[],remoteEvents:o=[],rootEvents:s=[],stateOwnersUpdateHandlers:l=[]}])=>{f[e]||(f[e]={});let d=n=>{let r=t(c,n);u({[e]:r})},m;m=f[e].mediaEvents,n.forEach(t=>{m&&ne&&(c.media.removeEventListener(t,m),f[e].mediaEvents=void 0),ce&&(p.media.addEventListener(t,d),f[e].mediaEvents=d)}),m=f[e].textTracksEvents,r.forEach(t=>{var n,r;m&&g&&((n=c.media.textTracks)==null||n.removeEventListener(t,m),f[e].textTracksEvents=void 0),le&&((r=p.media.textTracks)==null||r.addEventListener(t,d),f[e].textTracksEvents=d)}),m=f[e].videoRenditionsEvents,i.forEach(t=>{var n,r;m&&re&&((n=c.media.videoRenditions)==null||n.removeEventListener(t,m),f[e].videoRenditionsEvents=void 0),ue&&((r=p.media.videoRenditions)==null||r.addEventListener(t,d),f[e].videoRenditionsEvents=d)}),m=f[e].audioTracksEvents,a.forEach(t=>{var n,r;m&&ie&&((n=c.media.audioTracks)==null||n.removeEventListener(t,m),f[e].audioTracksEvents=void 0),de&&((r=p.media.audioTracks)==null||r.addEventListener(t,d),f[e].audioTracksEvents=d)}),m=f[e].remoteEvents,o.forEach(t=>{var n,r;m&&ae&&((n=c.media.remote)==null||n.removeEventListener(t,m),f[e].remoteEvents=void 0),fe&&((r=p.media.remote)==null||r.addEventListener(t,d),f[e].remoteEvents=d)}),m=f[e].rootEvents,s.forEach(t=>{m&&oe&&(c.documentElement.removeEventListener(t,m),f[e].rootEvents=void 0),pe&&(p.documentElement.addEventListener(t,d),f[e].rootEvents=d)});let h=f[e].stateOwnersUpdateHandlers;if(h&&se&&(Array.isArray(h)?h:[h]).forEach(e=>{typeof e==`function`&&e()}),me){let t=l.map(e=>e(d,p)).filter(e=>typeof e==`function`);f[e].stateOwnersUpdateHandlers=t.length===1?t[0]:t}else se&&(f[e].stateOwnersUpdateHandlers=void 0)}),Object.entries(p).forEach(([e,t])=>{c[e]=t}),d(),p=void 0};return m({media:e,fullscreenElement:t,documentElement:n,options:a}),{dispatch(e){let{type:t,detail:n}=e;if(i[t]&&l.mediaErrorCode==null){u(i[t](r,c,e));return}t===`mediaelementchangerequest`?m({media:n}):t===`fullscreenelementchangerequest`?m({fullscreenElement:n}):t===`documentelementchangerequest`?m({documentElement:n}):t===`optionschangerequest`&&(Object.entries(n??{}).forEach(([e,t])=>{c.options[e]=t}),d())},getState(){return l},subscribe(e){return m({},s.length+1),s.push(e),e(l),()=>{let t=s.indexOf(e);t>=0&&(m({},s.length-1),s.splice(t,1))}}}},Sn=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},I=(e,t,n)=>(Sn(e,t,`read from private field`),n?n.call(e):t.get(e)),L=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},R=(e,t,n,r)=>(Sn(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),Cn=(e,t,n)=>(Sn(e,t,`access private method`),n),wn,Tn,z,En,Dn,On,kn,An,jn,Mn,Nn,Pn,Fn,In,Ln,Rn=[`ArrowLeft`,`ArrowRight`,`ArrowUp`,`ArrowDown`,`Enter`,` `,`f`,`m`,`k`,`c`,`l`,`j`,`>`,`<`,`p`],zn=10,Bn=.025,Vn=.25,Hn=.25,Un=2,B={DEFAULT_SUBTITLES:`defaultsubtitles`,DEFAULT_STREAM_TYPE:`defaultstreamtype`,DEFAULT_DURATION:`defaultduration`,FULLSCREEN_ELEMENT:`fullscreenelement`,HOTKEYS:`hotkeys`,KEYBOARD_BACKWARD_SEEK_OFFSET:`keyboardbackwardseekoffset`,KEYBOARD_FORWARD_SEEK_OFFSET:`keyboardforwardseekoffset`,KEYBOARD_DOWN_VOLUME_STEP:`keyboarddownvolumestep`,KEYBOARD_UP_VOLUME_STEP:`keyboardupvolumestep`,KEYS_USED:`keysused`,LANG:`lang`,LOOP:`loop`,LIVE_EDGE_OFFSET:`liveedgeoffset`,NO_AUTO_SEEK_TO_LIVE:`noautoseektolive`,NO_DEFAULT_STORE:`nodefaultstore`,NO_HOTKEYS:`nohotkeys`,NO_MUTED_PREF:`nomutedpref`,NO_SUBTITLES_LANG_PREF:`nosubtitleslangpref`,NO_VOLUME_PREF:`novolumepref`,SEEK_TO_LIVE_OFFSET:`seektoliveoffset`},Wn=class extends yt{constructor(){super(),L(this,jn),L(this,Pn),L(this,In),this.mediaStateReceivers=[],this.associatedElementSubscriptions=new Map,L(this,wn,new kt(this,B.HOTKEYS)),L(this,Tn,void 0),L(this,z,void 0),L(this,En,null),L(this,Dn,void 0),L(this,On,void 0),L(this,kn,e=>{var t;(t=I(this,z))==null||t.dispatch(e)}),L(this,An,void 0),L(this,Nn,e=>{let{key:t,shiftKey:n}=e;if(!(n&&(t===`/`||t===`?`)||Rn.includes(t))){this.removeEventListener(`keyup`,I(this,Nn));return}this.keyboardShortcutHandler(e)}),this.associateElement(this);let e={};R(this,Dn,t=>{Object.entries(t).forEach(([t,n])=>{if(t in e&&e[t]===n)return;this.propagateMediaState(t,n);let r=t.toLowerCase(),i=new v.CustomEvent(o[r],{composed:!0,detail:n});this.dispatchEvent(i)}),e=t})}static get observedAttributes(){return super.observedAttributes.concat(B.NO_HOTKEYS,B.HOTKEYS,B.DEFAULT_STREAM_TYPE,B.DEFAULT_SUBTITLES,B.DEFAULT_DURATION,B.NO_MUTED_PREF,B.NO_VOLUME_PREF,B.LANG,B.LOOP,B.LIVE_EDGE_OFFSET,B.SEEK_TO_LIVE_OFFSET,B.NO_AUTO_SEEK_TO_LIVE)}get mediaStore(){return I(this,z)}set mediaStore(e){var t;if(I(this,z)&&((t=I(this,On))==null||t.call(this),R(this,On,void 0)),R(this,z,e),!I(this,z)&&!this.hasAttribute(B.NO_DEFAULT_STORE)){Cn(this,jn,Mn).call(this);return}R(this,On,I(this,z)?.subscribe(I(this,Dn)))}get fullscreenElement(){return I(this,Tn)??this}set fullscreenElement(e){var t;this.hasAttribute(B.FULLSCREEN_ELEMENT)&&this.removeAttribute(B.FULLSCREEN_ELEMENT),R(this,Tn,e),(t=I(this,z))==null||t.dispatch({type:`fullscreenelementchangerequest`,detail:this.fullscreenElement})}get defaultSubtitles(){return w(this,B.DEFAULT_SUBTITLES)}set defaultSubtitles(e){T(this,B.DEFAULT_SUBTITLES,e)}get defaultStreamType(){return E(this,B.DEFAULT_STREAM_TYPE)}set defaultStreamType(e){D(this,B.DEFAULT_STREAM_TYPE,e)}get defaultDuration(){return S(this,B.DEFAULT_DURATION)}set defaultDuration(e){C(this,B.DEFAULT_DURATION,e)}get noHotkeys(){return w(this,B.NO_HOTKEYS)}set noHotkeys(e){T(this,B.NO_HOTKEYS,e)}get keysUsed(){return E(this,B.KEYS_USED)}set keysUsed(e){D(this,B.KEYS_USED,e)}get liveEdgeOffset(){return S(this,B.LIVE_EDGE_OFFSET)}set liveEdgeOffset(e){C(this,B.LIVE_EDGE_OFFSET,e)}get noAutoSeekToLive(){return w(this,B.NO_AUTO_SEEK_TO_LIVE)}set noAutoSeekToLive(e){T(this,B.NO_AUTO_SEEK_TO_LIVE,e)}get noVolumePref(){return w(this,B.NO_VOLUME_PREF)}set noVolumePref(e){T(this,B.NO_VOLUME_PREF,e)}get noMutedPref(){return w(this,B.NO_MUTED_PREF)}set noMutedPref(e){T(this,B.NO_MUTED_PREF,e)}get noSubtitlesLangPref(){return w(this,B.NO_SUBTITLES_LANG_PREF)}set noSubtitlesLangPref(e){T(this,B.NO_SUBTITLES_LANG_PREF,e)}get noDefaultStore(){return w(this,B.NO_DEFAULT_STORE)}set noDefaultStore(e){T(this,B.NO_DEFAULT_STORE,e)}get resolvedLang(){return se()}attributeChangedCallback(t,n,r){var i,a,o,s,c,l,u,d,f,p;if(super.attributeChangedCallback(t,n,r),t===B.NO_HOTKEYS)r!==n&&r===``?(this.hasAttribute(B.HOTKEYS)&&console.warn("Media Chrome: Both `hotkeys` and `nohotkeys` have been set. All hotkeys will be disabled."),this.disableHotkeys()):r!==n&&r===null&&this.enableHotkeys();else if(t===B.HOTKEYS)I(this,wn).value=r;else if(t===B.DEFAULT_SUBTITLES&&r!==n)(i=I(this,z))==null||i.dispatch({type:`optionschangerequest`,detail:{defaultSubtitles:this.hasAttribute(B.DEFAULT_SUBTITLES)}});else if(t===B.DEFAULT_STREAM_TYPE)(a=I(this,z))==null||a.dispatch({type:`optionschangerequest`,detail:{defaultStreamType:this.getAttribute(B.DEFAULT_STREAM_TYPE)??void 0}});else if(t===B.LIVE_EDGE_OFFSET&&r!==n)(o=I(this,z))==null||o.dispatch({type:`optionschangerequest`,detail:{liveEdgeOffset:this.hasAttribute(B.LIVE_EDGE_OFFSET)?+this.getAttribute(B.LIVE_EDGE_OFFSET):void 0,seekToLiveOffset:this.hasAttribute(B.SEEK_TO_LIVE_OFFSET)?+this.getAttribute(B.SEEK_TO_LIVE_OFFSET):this.hasAttribute(B.LIVE_EDGE_OFFSET)?+this.getAttribute(B.LIVE_EDGE_OFFSET):void 0}});else if(t===B.SEEK_TO_LIVE_OFFSET&&r!==n)(s=I(this,z))==null||s.dispatch({type:`optionschangerequest`,detail:{seekToLiveOffset:this.hasAttribute(B.SEEK_TO_LIVE_OFFSET)?+this.getAttribute(B.SEEK_TO_LIVE_OFFSET):this.hasAttribute(B.LIVE_EDGE_OFFSET)?+this.getAttribute(B.LIVE_EDGE_OFFSET):void 0}});else if(t===B.NO_AUTO_SEEK_TO_LIVE)(c=I(this,z))==null||c.dispatch({type:`optionschangerequest`,detail:{noAutoSeekToLive:this.hasAttribute(B.NO_AUTO_SEEK_TO_LIVE)}});else if(t===B.FULLSCREEN_ELEMENT){let e=r?this.getRootNode()?.getElementById(r):void 0;R(this,Tn,e),(l=I(this,z))==null||l.dispatch({type:`fullscreenelementchangerequest`,detail:this.fullscreenElement})}else t===B.LANG&&r!==n?(ie(r),(u=I(this,z))==null||u.dispatch({type:`optionschangerequest`,detail:{mediaLang:r}})):t===B.LOOP&&r!==n?(d=I(this,z))==null||d.dispatch({type:e.MEDIA_LOOP_REQUEST,detail:r!=null}):t===B.NO_VOLUME_PREF&&r!==n?(f=I(this,z))==null||f.dispatch({type:`optionschangerequest`,detail:{noVolumePref:this.hasAttribute(B.NO_VOLUME_PREF)}}):t===B.NO_MUTED_PREF&&r!==n&&((p=I(this,z))==null||p.dispatch({type:`optionschangerequest`,detail:{noMutedPref:this.hasAttribute(B.NO_MUTED_PREF)}}))}connectedCallback(){var t,n;this.associateElement(this),!I(this,z)&&!this.hasAttribute(B.NO_DEFAULT_STORE)&&Cn(this,jn,Mn).call(this),(t=I(this,z))==null||t.dispatch({type:`documentelementchangerequest`,detail:y}),(n=I(this,z))==null||n.dispatch({type:`fullscreenelementchangerequest`,detail:this.fullscreenElement}),super.connectedCallback(),I(this,z)&&!I(this,On)&&R(this,On,I(this,z)?.subscribe(I(this,Dn))),I(this,An)!==void 0&&I(this,z)&&this.media&&setTimeout(()=>{var t;this.media?.textTracks?.length&&((t=I(this,z))==null||t.dispatch({type:e.MEDIA_TOGGLE_SUBTITLES_REQUEST,detail:I(this,An)}))},0),this.hasAttribute(B.NO_HOTKEYS)?this.disableHotkeys():this.enableHotkeys()}disconnectedCallback(){var t,n,r,i,a;if((t=super.disconnectedCallback)==null||t.call(this),this.disableHotkeys(),I(this,z)){let t=I(this,z).getState();R(this,An,!!t.mediaSubtitlesShowing?.length),(n=I(this,z))==null||n.dispatch({type:`fullscreenelementchangerequest`,detail:void 0}),(r=I(this,z))==null||r.dispatch({type:`documentelementchangerequest`,detail:void 0}),(i=I(this,z))==null||i.dispatch({type:e.MEDIA_TOGGLE_SUBTITLES_REQUEST,detail:!1})}I(this,On)&&((a=I(this,On))==null||a.call(this),R(this,On,void 0)),this.unassociateElement(this),I(this,En)&&(I(this,En).remove(),R(this,En,null))}mediaSetCallback(e){var t;super.mediaSetCallback(e),(t=I(this,z))==null||t.dispatch({type:`mediaelementchangerequest`,detail:e}),e.hasAttribute(`tabindex`)||(e.tabIndex=-1)}mediaUnsetCallback(e){var t;super.mediaUnsetCallback(e),(t=I(this,z))==null||t.dispatch({type:`mediaelementchangerequest`,detail:void 0})}propagateMediaState(e,t){tr(this.mediaStateReceivers,e,t)}associateElement(t){if(!t)return;let{associatedElementSubscriptions:n}=this;if(n.has(t))return;let r=nr(t,this.registerMediaStateReceiver.bind(this),this.unregisterMediaStateReceiver.bind(this));Object.values(e).forEach(e=>{t.addEventListener(e,I(this,kn))}),n.set(t,r)}unassociateElement(t){if(!t)return;let{associatedElementSubscriptions:n}=this;n.has(t)&&(n.get(t)(),n.delete(t),Object.values(e).forEach(e=>{t.removeEventListener(e,I(this,kn))}))}registerMediaStateReceiver(e){if(!e)return;let t=this.mediaStateReceivers;t.indexOf(e)>-1||(t.push(e),I(this,z)&&Object.entries(I(this,z).getState()).forEach(([t,n])=>{tr([e],t,n)}))}unregisterMediaStateReceiver(e){let t=this.mediaStateReceivers,n=t.indexOf(e);n<0||t.splice(n,1)}enableHotkeys(){this.addEventListener(`keydown`,Cn(this,Pn,Fn))}disableHotkeys(){this.removeEventListener(`keydown`,Cn(this,Pn,Fn)),this.removeEventListener(`keyup`,I(this,Nn))}get hotkeys(){return I(this,wn)}set hotkeys(e){D(this,B.HOTKEYS,e)}keyboardShortcutHandler(t){let n=t.target;if((n.getAttribute(B.KEYS_USED)?.split(` `)??n?.keysUsed??[]).map(e=>e===`Space`?` `:e).filter(Boolean).includes(t.key))return;let r,i,a;if(!I(this,wn).contains(`no${t.key.toLowerCase()}`)&&!(t.key===` `&&I(this,wn).contains(`nospace`))&&!(t.shiftKey&&(t.key===`/`||t.key===`?`)&&I(this,wn).contains(`noshift+/`)))switch(t.key){case` `:case`k`:r=I(this,z).getState().mediaPaused?e.MEDIA_PLAY_REQUEST:e.MEDIA_PAUSE_REQUEST,this.dispatchEvent(new v.CustomEvent(r,{composed:!0,bubbles:!0}));break;case`m`:r=this.mediaStore.getState().mediaVolumeLevel===`off`?e.MEDIA_UNMUTE_REQUEST:e.MEDIA_MUTE_REQUEST,this.dispatchEvent(new v.CustomEvent(r,{composed:!0,bubbles:!0}));break;case`f`:r=this.mediaStore.getState().mediaIsFullscreen?e.MEDIA_EXIT_FULLSCREEN_REQUEST:e.MEDIA_ENTER_FULLSCREEN_REQUEST,this.dispatchEvent(new v.CustomEvent(r,{composed:!0,bubbles:!0}));break;case`c`:this.dispatchEvent(new v.CustomEvent(e.MEDIA_TOGGLE_SUBTITLES_REQUEST,{composed:!0,bubbles:!0}));break;case`ArrowLeft`:case`j`:{let t=this.hasAttribute(B.KEYBOARD_BACKWARD_SEEK_OFFSET)?+this.getAttribute(B.KEYBOARD_BACKWARD_SEEK_OFFSET):zn;i=Math.max((this.mediaStore.getState().mediaCurrentTime??0)-t,0),a=new v.CustomEvent(e.MEDIA_SEEK_REQUEST,{composed:!0,bubbles:!0,detail:i}),this.dispatchEvent(a);break}case`ArrowRight`:case`l`:{let t=this.hasAttribute(B.KEYBOARD_FORWARD_SEEK_OFFSET)?+this.getAttribute(B.KEYBOARD_FORWARD_SEEK_OFFSET):zn;i=Math.max((this.mediaStore.getState().mediaCurrentTime??0)+t,0),a=new v.CustomEvent(e.MEDIA_SEEK_REQUEST,{composed:!0,bubbles:!0,detail:i}),this.dispatchEvent(a);break}case`ArrowUp`:{let t=this.hasAttribute(B.KEYBOARD_UP_VOLUME_STEP)?+this.getAttribute(B.KEYBOARD_UP_VOLUME_STEP):Bn;i=Math.min((this.mediaStore.getState().mediaVolume??1)+t,1),a=new v.CustomEvent(e.MEDIA_VOLUME_REQUEST,{composed:!0,bubbles:!0,detail:i}),this.dispatchEvent(a);break}case`ArrowDown`:{let t=this.hasAttribute(B.KEYBOARD_DOWN_VOLUME_STEP)?+this.getAttribute(B.KEYBOARD_DOWN_VOLUME_STEP):Bn;i=Math.max((this.mediaStore.getState().mediaVolume??1)-t,0),a=new v.CustomEvent(e.MEDIA_VOLUME_REQUEST,{composed:!0,bubbles:!0,detail:i}),this.dispatchEvent(a);break}case`<`:{let t=this.mediaStore.getState().mediaPlaybackRate??1;i=Math.max(t-Vn,Hn).toFixed(2),a=new v.CustomEvent(e.MEDIA_PLAYBACK_RATE_REQUEST,{composed:!0,bubbles:!0,detail:i}),this.dispatchEvent(a);break}case`>`:{let t=this.mediaStore.getState().mediaPlaybackRate??1;i=Math.min(t+Vn,Un).toFixed(2),a=new v.CustomEvent(e.MEDIA_PLAYBACK_RATE_REQUEST,{composed:!0,bubbles:!0,detail:i}),this.dispatchEvent(a);break}case`/`:case`?`:t.shiftKey&&Cn(this,In,Ln).call(this);break;case`p`:r=this.mediaStore.getState().mediaIsPip?e.MEDIA_EXIT_PIP_REQUEST:e.MEDIA_ENTER_PIP_REQUEST,a=new v.CustomEvent(r,{composed:!0,bubbles:!0}),this.dispatchEvent(a)}}};wn=new WeakMap,Tn=new WeakMap,z=new WeakMap,En=new WeakMap,Dn=new WeakMap,On=new WeakMap,kn=new WeakMap,An=new WeakMap,jn=new WeakSet,Mn=function(){this.mediaStore=xn({media:this.media,fullscreenElement:this.fullscreenElement,options:{defaultSubtitles:this.hasAttribute(B.DEFAULT_SUBTITLES),defaultDuration:this.hasAttribute(B.DEFAULT_DURATION)?+this.getAttribute(B.DEFAULT_DURATION):void 0,defaultStreamType:this.getAttribute(B.DEFAULT_STREAM_TYPE)??void 0,liveEdgeOffset:this.hasAttribute(B.LIVE_EDGE_OFFSET)?+this.getAttribute(B.LIVE_EDGE_OFFSET):void 0,seekToLiveOffset:this.hasAttribute(B.SEEK_TO_LIVE_OFFSET)?+this.getAttribute(B.SEEK_TO_LIVE_OFFSET):this.hasAttribute(B.LIVE_EDGE_OFFSET)?+this.getAttribute(B.LIVE_EDGE_OFFSET):void 0,noAutoSeekToLive:this.hasAttribute(B.NO_AUTO_SEEK_TO_LIVE),noVolumePref:this.hasAttribute(B.NO_VOLUME_PREF),noMutedPref:this.hasAttribute(B.NO_MUTED_PREF),noSubtitlesLangPref:this.hasAttribute(B.NO_SUBTITLES_LANG_PREF)}})},Nn=new WeakMap,Pn=new WeakSet,Fn=function(e){let{metaKey:t,altKey:n,key:r,shiftKey:i}=e,a=i&&(r===`/`||r===`?`);if(a&&I(this,En)?.open){this.removeEventListener(`keyup`,I(this,Nn));return}if(t||n||!a&&!Rn.includes(r)){this.removeEventListener(`keyup`,I(this,Nn));return}let o=e.target,s=o instanceof HTMLElement&&(o.tagName.toLowerCase()===`media-volume-range`||o.tagName.toLowerCase()===`media-time-range`);[` `,`ArrowLeft`,`ArrowRight`,`ArrowUp`,`ArrowDown`].includes(r)&&!(I(this,wn).contains(`no${r.toLowerCase()}`)||r===` `&&I(this,wn).contains(`nospace`))&&!s&&e.preventDefault(),this.addEventListener(`keyup`,I(this,Nn),{once:!0})},In=new WeakSet,Ln=function(){I(this,En)||(R(this,En,y.createElement(`media-keyboard-shortcuts-dialog`)),this.appendChild(I(this,En))),I(this,En).open=!0};var Gn=Object.values(i),Kn=Object.values(n),qn=e=>{var n;let{observedAttributes:r}=e.constructor;!r&&e.nodeName?.includes(`-`)&&(v.customElements.upgrade(e),{observedAttributes:r}=e.constructor);let i=((n=(e?.getAttribute)?.call(e,t.MEDIA_CHROME_ATTRIBUTES))?.split)?.call(n,/\s+/);return Array.isArray(r||i)?(r||i).filter(e=>Gn.includes(e)):[]},Jn=e=>(e.nodeName?.includes(`-`)&&v.customElements.get(e.nodeName?.toLowerCase())&&!(e instanceof v.customElements.get(e.nodeName.toLowerCase()))&&v.customElements.upgrade(e),Kn.some(t=>t in e)),Yn=e=>Jn(e)||!!qn(e).length,Xn=e=>(e?.join)?.call(e,`:`),Zn={[i.MEDIA_SUBTITLES_LIST]:Ft,[i.MEDIA_SUBTITLES_SHOWING]:Ft,[i.MEDIA_SEEKABLE]:Xn,[i.MEDIA_BUFFERED]:e=>e?.map(Xn).join(` `),[i.MEDIA_PREVIEW_COORDS]:e=>e?.join(` `),[i.MEDIA_RENDITION_LIST]:p,[i.MEDIA_AUDIO_TRACK_LIST]:h},Qn=async(e,t,n)=>{if(e.isConnected||await ne(0),typeof n==`boolean`||n==null)return T(e,t,n);if(typeof n==`number`)return C(e,t,n);if(typeof n==`string`)return D(e,t,n);if(Array.isArray(n)&&!n.length)return e.removeAttribute(t);let r=Zn[t]?.call(Zn,n)??n;return e.setAttribute(t,r)},$n=e=>!!e.closest?.call(e,`*[slot="media"]`),er=(e,t)=>{if($n(e))return;let n=(e,t)=>{Yn(e)&&t(e);let{children:n=[]}=e??{},r=e?.shadowRoot?.children??[];[...n,...r].forEach(e=>er(e,t))},r=e?.nodeName.toLowerCase();if(r.includes(`-`)&&!Yn(e)){v.customElements.whenDefined(r).then(()=>{n(e,t)});return}n(e,t)},tr=(e,t,n)=>{e.forEach(e=>{if(t in e){e[t]=n;return}let r=qn(e),i=t.toLowerCase();r.includes(i)&&Qn(e,i,n)})},nr=(n,r,i)=>{er(n,r);let a=e=>{r(e?.composedPath()[0]??e.target)},o=e=>{i(e?.composedPath()[0]??e.target)};n.addEventListener(e.REGISTER_MEDIA_STATE_RECEIVER,a),n.addEventListener(e.UNREGISTER_MEDIA_STATE_RECEIVER,o);let s=e=>{e.forEach(e=>{let{addedNodes:n=[],removedNodes:a=[],type:o,target:s,attributeName:c}=e;o===`childList`?(Array.prototype.forEach.call(n,e=>er(e,r)),Array.prototype.forEach.call(a,e=>er(e,i))):o===`attributes`&&c===t.MEDIA_CHROME_ATTRIBUTES&&(Yn(s)?r(s):i(s))})},c=[],l=e=>{let t=e.target;t.name!==`media`&&(c.forEach(e=>er(e,i)),c=[...t.assignedElements({flatten:!0})],c.forEach(e=>er(e,r)))};n.addEventListener(`slotchange`,l);let u=new MutationObserver(s);return u.observe(n,{childList:!0,attributes:!0,subtree:!0}),()=>{er(n,i),n.removeEventListener(`slotchange`,l),u.disconnect(),n.removeEventListener(e.REGISTER_MEDIA_STATE_RECEIVER,a),n.removeEventListener(e.UNREGISTER_MEDIA_STATE_RECEIVER,o)}};v.customElements.get(`media-controller`)||v.customElements.define(`media-controller`,Wn);var rr={PLACEMENT:`placement`,BOUNDS:`bounds`};function ir(e){return`
    <style>
      :host {
        --_tooltip-background-color: var(--media-tooltip-background-color, var(--media-secondary-color, rgba(20, 20, 30, .7)));
        --_tooltip-background: var(--media-tooltip-background, var(--_tooltip-background-color));
        --_tooltip-arrow-half-width: calc(var(--media-tooltip-arrow-width, 12px) / 2);
        --_tooltip-arrow-height: var(--media-tooltip-arrow-height, 5px);
        --_tooltip-arrow-background: var(--media-tooltip-arrow-color, var(--_tooltip-background-color));
        position: relative;
        pointer-events: none;
        display: var(--media-tooltip-display, inline-flex);
        justify-content: center;
        align-items: center;
        box-sizing: border-box;
        z-index: var(--media-tooltip-z-index, 1);
        background: var(--_tooltip-background);
        color: var(--media-text-color, var(--media-primary-color, rgb(238 238 238)));
        font: var(--media-font,
          var(--media-font-weight, 400)
          var(--media-font-size, 13px) /
          var(--media-text-content-height, var(--media-control-height, 18px))
          var(--media-font-family, helvetica neue, segoe ui, roboto, arial, sans-serif));
        padding: var(--media-tooltip-padding, .35em .7em);
        border: var(--media-tooltip-border, none);
        border-radius: var(--media-tooltip-border-radius, 5px);
        filter: var(--media-tooltip-filter, drop-shadow(0 0 4px rgba(0, 0, 0, .2)));
        white-space: var(--media-tooltip-white-space, nowrap);
      }

      :host([hidden]) {
        display: none;
      }

      img, svg {
        display: inline-block;
      }

      #arrow {
        position: absolute;
        width: 0px;
        height: 0px;
        border-style: solid;
        display: var(--media-tooltip-arrow-display, block);
      }

      :host(:not([placement])),
      :host([placement="top"]) {
        position: absolute;
        bottom: calc(100% + var(--media-tooltip-distance, 12px));
        left: 50%;
        transform: translate(calc(-50% - var(--media-tooltip-offset-x, 0px)), 0);
      }
      :host(:not([placement])) #arrow,
      :host([placement="top"]) #arrow {
        top: 100%;
        left: 50%;
        border-width: var(--_tooltip-arrow-height) var(--_tooltip-arrow-half-width) 0 var(--_tooltip-arrow-half-width);
        border-color: var(--_tooltip-arrow-background) transparent transparent transparent;
        transform: translate(calc(-50% + var(--media-tooltip-offset-x, 0px)), 0);
      }

      :host([placement="right"]) {
        position: absolute;
        left: calc(100% + var(--media-tooltip-distance, 12px));
        top: 50%;
        transform: translate(0, -50%);
      }
      :host([placement="right"]) #arrow {
        top: 50%;
        right: 100%;
        border-width: var(--_tooltip-arrow-half-width) var(--_tooltip-arrow-height) var(--_tooltip-arrow-half-width) 0;
        border-color: transparent var(--_tooltip-arrow-background) transparent transparent;
        transform: translate(0, -50%);
      }

      :host([placement="bottom"]) {
        position: absolute;
        top: calc(100% + var(--media-tooltip-distance, 12px));
        left: 50%;
        transform: translate(calc(-50% - var(--media-tooltip-offset-x, 0px)), 0);
      }
      :host([placement="bottom"]) #arrow {
        bottom: 100%;
        left: 50%;
        border-width: 0 var(--_tooltip-arrow-half-width) var(--_tooltip-arrow-height) var(--_tooltip-arrow-half-width);
        border-color: transparent transparent var(--_tooltip-arrow-background) transparent;
        transform: translate(calc(-50% + var(--media-tooltip-offset-x, 0px)), 0);
      }

      :host([placement="left"]) {
        position: absolute;
        right: calc(100% + var(--media-tooltip-distance, 12px));
        top: 50%;
        transform: translate(0, -50%);
      }
      :host([placement="left"]) #arrow {
        top: 50%;
        left: 100%;
        border-width: var(--_tooltip-arrow-half-width) 0 var(--_tooltip-arrow-half-width) var(--_tooltip-arrow-height);
        border-color: transparent transparent transparent var(--_tooltip-arrow-background);
        transform: translate(0, -50%);
      }
      
      :host([placement="none"]) #arrow {
        display: none;
      }
    </style>
    <slot></slot>
    <div id="arrow"></div>
  `}var ar=class extends v.HTMLElement{constructor(){if(super(),this.updateXOffset=()=>{if(!Pe(this,{checkOpacity:!1,checkVisibilityCSS:!1}))return;let e=this.placement;if(e===`left`||e===`right`){this.style.removeProperty(`--media-tooltip-offset-x`);return}let t=getComputedStyle(this),n=je(this,`#`+this.bounds)??Te(this);if(!n)return;let{x:r,width:i}=n.getBoundingClientRect(),{x:a,width:o}=this.getBoundingClientRect(),s=a+o,c=r+i,l=t.getPropertyValue(`--media-tooltip-offset-x`),u=l?parseFloat(l.replace(`px`,``)):0,d=t.getPropertyValue(`--media-tooltip-container-margin`),f=d?parseFloat(d.replace(`px`,``)):0,p=a-r+u-f,m=s-c+u+f;if(p<0){this.style.setProperty(`--media-tooltip-offset-x`,`${p}px`);return}if(m>0){this.style.setProperty(`--media-tooltip-offset-x`,`${m}px`);return}this.style.removeProperty(`--media-tooltip-offset-x`)},!this.shadowRoot){this.attachShadow(this.constructor.shadowRootOptions);let e=b(this.attributes);this.shadowRoot.innerHTML=this.constructor.getTemplateHTML(e)}if(this.arrowEl=this.shadowRoot.querySelector(`#arrow`),Object.prototype.hasOwnProperty.call(this,`placement`)){let e=this.placement;delete this.placement,this.placement=e}}static get observedAttributes(){return[rr.PLACEMENT,rr.BOUNDS]}get placement(){return E(this,rr.PLACEMENT)}set placement(e){D(this,rr.PLACEMENT,e)}get bounds(){return E(this,rr.BOUNDS)}set bounds(e){D(this,rr.BOUNDS,e)}};ar.shadowRootOptions={mode:`open`},ar.getTemplateHTML=ir,v.customElements.get(`media-tooltip`)||v.customElements.define(`media-tooltip`,ar);var or=ar,sr=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},V=(e,t,n)=>(sr(e,t,`read from private field`),n?n.call(e):t.get(e)),cr=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},lr=(e,t,n,r)=>(sr(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),ur=(e,t,n)=>(sr(e,t,`access private method`),n),dr,fr,pr,mr,hr,gr,_r,vr={TOOLTIP_PLACEMENT:`tooltipplacement`,DISABLED:`disabled`,NO_TOOLTIP:`notooltip`};function yr(e,t={}){return`
    <style>
      :host {
        position: relative;
        font: var(--media-font,
          var(--media-font-weight, bold)
          var(--media-font-size, 14px) /
          var(--media-text-content-height, var(--media-control-height, 24px))
          var(--media-font-family, helvetica neue, segoe ui, roboto, arial, sans-serif));
        color: var(--media-text-color, var(--media-primary-color, rgb(238 238 238)));
        background: var(--media-control-background, var(--media-secondary-color, rgb(20 20 30 / .7)));
        padding: var(--media-button-padding, var(--media-control-padding, 10px));
        justify-content: var(--media-button-justify-content, center);
        display: inline-flex;
        align-items: center;
        vertical-align: middle;
        box-sizing: border-box;
        transition: background .15s linear;
        pointer-events: auto;
        cursor: var(--media-cursor, pointer);
        -webkit-tap-highlight-color: transparent;
      }

      
      :host(:focus-visible) {
        box-shadow: var(--media-focus-box-shadow, inset 0 0 0 2px rgb(27 127 204 / .9));
        outline: 0;
      }
      
      :host(:where(:focus)) {
        box-shadow: none;
        outline: 0;
      }

      :host(:hover) {
        background: var(--media-control-hover-background, rgba(50 50 70 / .7));
      }

      slot[name="icon"] {
        display: inline-flex;
        align-items: center;
      }

      svg, img, ::slotted(svg), ::slotted(img) {
        width: var(--media-button-icon-width);
        height: var(--media-button-icon-height, var(--media-control-height, 24px));
        transform: var(--media-button-icon-transform);
        transition: var(--media-button-icon-transition);
        fill: var(--media-icon-color, var(--media-primary-color, rgb(238 238 238)));
        vertical-align: middle;
        max-width: 100%;
        max-height: 100%;
        min-width: 100%;
      }

      media-tooltip {
        
        max-width: 0;
        overflow-x: clip;
        opacity: 0;
        transition: opacity .3s, max-width 0s 9s;
      }

      :host(:hover) media-tooltip,
      :host(:focus-visible) media-tooltip {
        max-width: 100vw;
        opacity: 1;
        transition: opacity .3s;
      }

      :host([notooltip]) slot[name="tooltip"] {
        display: none;
      }
    </style>

    ${this.getSlotTemplateHTML(e,t)}

    <slot name="tooltip">
      <media-tooltip part="tooltip" aria-hidden="true">
        <template shadowrootmode="${or.shadowRootOptions.mode}">
          ${or.getTemplateHTML({})}
        </template>
        <slot name="tooltip-content">
          ${this.getTooltipContentHTML(e)}
        </slot>
      </media-tooltip>
    </slot>
  `}function br(e,t){return`
    <slot></slot>
  `}function xr(){return``}var H=class extends v.HTMLElement{constructor(){if(super(),cr(this,gr),cr(this,dr,void 0),this.preventClick=!1,this.tooltipEl=null,cr(this,fr,e=>{this.preventClick||this.handleClick(e),setTimeout(V(this,pr),0)}),cr(this,pr,()=>{var e,t;(t=(e=this.tooltipEl)?.updateXOffset)==null||t.call(e)}),cr(this,mr,e=>{let{key:t}=e;if(!this.keysUsed.includes(t)){this.removeEventListener(`keyup`,V(this,mr));return}this.preventClick||this.handleClick(e)}),cr(this,hr,e=>{let{metaKey:t,altKey:n,key:r}=e;if(t||n||!this.keysUsed.includes(r)){this.removeEventListener(`keyup`,V(this,mr));return}this.addEventListener(`keyup`,V(this,mr),{once:!0})}),!this.shadowRoot){this.attachShadow(this.constructor.shadowRootOptions);let e=b(this.attributes),t=this.constructor.getTemplateHTML(e);this.shadowRoot.setHTMLUnsafe?this.shadowRoot.setHTMLUnsafe(t):this.shadowRoot.innerHTML=t}this.tooltipEl=this.shadowRoot.querySelector(`media-tooltip`)}static get observedAttributes(){return[`disabled`,vr.TOOLTIP_PLACEMENT,t.MEDIA_CONTROLLER,i.MEDIA_LANG]}enable(){this.addEventListener(`click`,V(this,fr)),this.addEventListener(`keydown`,V(this,hr)),this.tabIndex=0}disable(){this.removeEventListener(`click`,V(this,fr)),this.removeEventListener(`keydown`,V(this,hr)),this.removeEventListener(`keyup`,V(this,mr)),this.tabIndex=-1}attributeChangedCallback(e,n,r){var a,o,s,c;e===t.MEDIA_CONTROLLER?(n&&((o=(a=V(this,dr))?.unassociateElement)==null||o.call(a,this),lr(this,dr,null)),r&&this.isConnected&&(lr(this,dr,this.getRootNode()?.getElementById(r)),(c=(s=V(this,dr))?.associateElement)==null||c.call(s,this))):e===`disabled`&&r!==n?r==null?this.enable():this.disable():e===vr.TOOLTIP_PLACEMENT&&this.tooltipEl&&r!==n?this.tooltipEl.placement=r:e===i.MEDIA_LANG&&(this.shadowRoot.querySelector(`slot[name="tooltip-content"]`).innerHTML=this.constructor.getTooltipContentHTML()),V(this,pr).call(this)}connectedCallback(){var e,n;let{style:r}=x(this.shadowRoot,`:host`);r.setProperty(`display`,`var(--media-control-display, var(--${this.localName}-display, inline-flex))`),this.hasAttribute(`disabled`)?this.disable():this.enable(),this.setAttribute(`role`,`button`);let i=this.getAttribute(t.MEDIA_CONTROLLER);i&&(lr(this,dr,this.getRootNode()?.getElementById(i)),(n=(e=V(this,dr))?.associateElement)==null||n.call(e,this)),v.customElements.whenDefined(`media-tooltip`).then(()=>ur(this,gr,_r).call(this))}disconnectedCallback(){var e,t;this.disable(),(t=(e=V(this,dr))?.unassociateElement)==null||t.call(e,this),lr(this,dr,null),this.removeEventListener(`mouseenter`,V(this,pr)),this.removeEventListener(`focus`,V(this,pr)),this.removeEventListener(`click`,V(this,fr))}get keysUsed(){return[`Enter`,` `]}get tooltipPlacement(){return E(this,vr.TOOLTIP_PLACEMENT)}set tooltipPlacement(e){D(this,vr.TOOLTIP_PLACEMENT,e)}get mediaController(){return E(this,t.MEDIA_CONTROLLER)}set mediaController(e){D(this,t.MEDIA_CONTROLLER,e)}get disabled(){return w(this,vr.DISABLED)}set disabled(e){T(this,vr.DISABLED,e)}get noTooltip(){return w(this,vr.NO_TOOLTIP)}set noTooltip(e){T(this,vr.NO_TOOLTIP,e)}handleClick(e){}};dr=new WeakMap,fr=new WeakMap,pr=new WeakMap,mr=new WeakMap,hr=new WeakMap,gr=new WeakSet,_r=function(){this.addEventListener(`mouseenter`,V(this,pr)),this.addEventListener(`focus`,V(this,pr)),this.addEventListener(`click`,V(this,fr));let e=this.tooltipPlacement;e&&this.tooltipEl&&(this.tooltipEl.placement=e)},H.shadowRootOptions={mode:`open`},H.getTemplateHTML=yr,H.getSlotTemplateHTML=br,H.getTooltipContentHTML=xr,v.customElements.get(`media-chrome-button`)||v.customElements.define(`media-chrome-button`,H);var Sr=`<svg aria-hidden="true" viewBox="0 0 26 24">
  <path d="M22.13 3H3.87a.87.87 0 0 0-.87.87v13.26a.87.87 0 0 0 .87.87h3.4L9 16H5V5h16v11h-4l1.72 2h3.4a.87.87 0 0 0 .87-.87V3.87a.87.87 0 0 0-.86-.87Zm-8.75 11.44a.5.5 0 0 0-.76 0l-4.91 5.73a.5.5 0 0 0 .38.83h9.82a.501.501 0 0 0 .38-.83l-4.91-5.73Z"/>
</svg>
`;function Cr(e){return`
    <style>
      :host([${i.MEDIA_IS_AIRPLAYING}]) slot[name=icon] slot:not([name=exit]) {
        display: none !important;
      }

      
      :host(:not([${i.MEDIA_IS_AIRPLAYING}])) slot[name=icon] slot:not([name=enter]) {
        display: none !important;
      }

      :host([${i.MEDIA_IS_AIRPLAYING}]) slot[name=tooltip-enter],
      :host(:not([${i.MEDIA_IS_AIRPLAYING}])) slot[name=tooltip-exit] {
        display: none;
      }
    </style>

    <slot name="icon">
      <slot name="enter">${Sr}</slot>
      <slot name="exit">${Sr}</slot>
    </slot>
  `}function wr(){return`
    <slot name="tooltip-enter">${_(`start airplay`)}</slot>
    <slot name="tooltip-exit">${_(`stop airplay`)}</slot>
  `}var Tr=e=>{let t=e.mediaIsAirplaying?_(`stop airplay`):_(`start airplay`);e.setAttribute(`aria-label`,t)},Er=class extends H{static get observedAttributes(){return[...super.observedAttributes,i.MEDIA_IS_AIRPLAYING,i.MEDIA_AIRPLAY_UNAVAILABLE]}connectedCallback(){super.connectedCallback(),Tr(this)}attributeChangedCallback(e,t,n){super.attributeChangedCallback(e,t,n),e===i.MEDIA_IS_AIRPLAYING&&Tr(this)}get mediaIsAirplaying(){return w(this,i.MEDIA_IS_AIRPLAYING)}set mediaIsAirplaying(e){T(this,i.MEDIA_IS_AIRPLAYING,e)}get mediaAirplayUnavailable(){return E(this,i.MEDIA_AIRPLAY_UNAVAILABLE)}set mediaAirplayUnavailable(e){D(this,i.MEDIA_AIRPLAY_UNAVAILABLE,e)}handleClick(){let t=new v.CustomEvent(e.MEDIA_AIRPLAY_REQUEST,{composed:!0,bubbles:!0});this.dispatchEvent(t)}};Er.getSlotTemplateHTML=Cr,Er.getTooltipContentHTML=wr,v.customElements.get(`media-airplay-button`)||v.customElements.define(`media-airplay-button`,Er);var Dr=`<svg aria-hidden="true" viewBox="0 0 26 24">
  <path d="M22.83 5.68a2.58 2.58 0 0 0-2.3-2.5c-3.62-.24-11.44-.24-15.06 0a2.58 2.58 0 0 0-2.3 2.5c-.23 4.21-.23 8.43 0 12.64a2.58 2.58 0 0 0 2.3 2.5c3.62.24 11.44.24 15.06 0a2.58 2.58 0 0 0 2.3-2.5c.23-4.21.23-8.43 0-12.64Zm-11.39 9.45a3.07 3.07 0 0 1-1.91.57 3.06 3.06 0 0 1-2.34-1 3.75 3.75 0 0 1-.92-2.67 3.92 3.92 0 0 1 .92-2.77 3.18 3.18 0 0 1 2.43-1 2.94 2.94 0 0 1 2.13.78c.364.359.62.813.74 1.31l-1.43.35a1.49 1.49 0 0 0-1.51-1.17 1.61 1.61 0 0 0-1.29.58 2.79 2.79 0 0 0-.5 1.89 3 3 0 0 0 .49 1.93 1.61 1.61 0 0 0 1.27.58 1.48 1.48 0 0 0 1-.37 2.1 2.1 0 0 0 .59-1.14l1.4.44a3.23 3.23 0 0 1-1.07 1.69Zm7.22 0a3.07 3.07 0 0 1-1.91.57 3.06 3.06 0 0 1-2.34-1 3.75 3.75 0 0 1-.92-2.67 3.88 3.88 0 0 1 .93-2.77 3.14 3.14 0 0 1 2.42-1 3 3 0 0 1 2.16.82 2.8 2.8 0 0 1 .73 1.31l-1.43.35a1.49 1.49 0 0 0-1.51-1.21 1.61 1.61 0 0 0-1.29.58A2.79 2.79 0 0 0 15 12a3 3 0 0 0 .49 1.93 1.61 1.61 0 0 0 1.27.58 1.44 1.44 0 0 0 1-.37 2.1 2.1 0 0 0 .6-1.15l1.4.44a3.17 3.17 0 0 1-1.1 1.7Z"/>
</svg>`,Or=`<svg aria-hidden="true" viewBox="0 0 26 24">
  <path d="M17.73 14.09a1.4 1.4 0 0 1-1 .37 1.579 1.579 0 0 1-1.27-.58A3 3 0 0 1 15 12a2.8 2.8 0 0 1 .5-1.85 1.63 1.63 0 0 1 1.29-.57 1.47 1.47 0 0 1 1.51 1.2l1.43-.34A2.89 2.89 0 0 0 19 9.07a3 3 0 0 0-2.14-.78 3.14 3.14 0 0 0-2.42 1 3.91 3.91 0 0 0-.93 2.78 3.74 3.74 0 0 0 .92 2.66 3.07 3.07 0 0 0 2.34 1 3.07 3.07 0 0 0 1.91-.57 3.17 3.17 0 0 0 1.07-1.74l-1.4-.45c-.083.43-.3.822-.62 1.12Zm-7.22 0a1.43 1.43 0 0 1-1 .37 1.58 1.58 0 0 1-1.27-.58A3 3 0 0 1 7.76 12a2.8 2.8 0 0 1 .5-1.85 1.63 1.63 0 0 1 1.29-.57 1.47 1.47 0 0 1 1.51 1.2l1.43-.34a2.81 2.81 0 0 0-.74-1.32 2.94 2.94 0 0 0-2.13-.78 3.18 3.18 0 0 0-2.43 1 4 4 0 0 0-.92 2.78 3.74 3.74 0 0 0 .92 2.66 3.07 3.07 0 0 0 2.34 1 3.07 3.07 0 0 0 1.91-.57 3.23 3.23 0 0 0 1.07-1.74l-1.4-.45a2.06 2.06 0 0 1-.6 1.07Zm12.32-8.41a2.59 2.59 0 0 0-2.3-2.51C18.72 3.05 15.86 3 13 3c-2.86 0-5.72.05-7.53.17a2.59 2.59 0 0 0-2.3 2.51c-.23 4.207-.23 8.423 0 12.63a2.57 2.57 0 0 0 2.3 2.5c1.81.13 4.67.19 7.53.19 2.86 0 5.72-.06 7.53-.19a2.57 2.57 0 0 0 2.3-2.5c.23-4.207.23-8.423 0-12.63Zm-1.49 12.53a1.11 1.11 0 0 1-.91 1.11c-1.67.11-4.45.18-7.43.18-2.98 0-5.76-.07-7.43-.18a1.11 1.11 0 0 1-.91-1.11c-.21-4.14-.21-8.29 0-12.43a1.11 1.11 0 0 1 .91-1.11C7.24 4.56 10 4.49 13 4.49s5.76.07 7.43.18a1.11 1.11 0 0 1 .91 1.11c.21 4.14.21 8.29 0 12.43Z"/>
</svg>`;function kr(e){return`
    <style>
      :host([aria-checked="true"]) slot[name=off] {
        display: none !important;
      }

      
      :host(:not([aria-checked="true"])) slot[name=on] {
        display: none !important;
      }

      :host([aria-checked="true"]) slot[name=tooltip-enable],
      :host(:not([aria-checked="true"])) slot[name=tooltip-disable] {
        display: none;
      }
    </style>

    <slot name="icon">
      <slot name="on">${Dr}</slot>
      <slot name="off">${Or}</slot>
    </slot>
  `}function Ar(){return`
    <slot name="tooltip-enable">${_(`Enable captions`)}</slot>
    <slot name="tooltip-disable">${_(`Disable captions`)}</slot>
  `}var jr=e=>{e.setAttribute(`aria-checked`,Bt(e).toString())},Mr=class extends H{static get observedAttributes(){return[...super.observedAttributes,i.MEDIA_SUBTITLES_LIST,i.MEDIA_SUBTITLES_SHOWING]}connectedCallback(){super.connectedCallback(),this.setAttribute(`role`,`button`),this.setAttribute(`aria-label`,_(`closed captions`)),jr(this)}attributeChangedCallback(e,t,n){super.attributeChangedCallback(e,t,n),e===i.MEDIA_SUBTITLES_SHOWING&&jr(this)}get mediaSubtitlesList(){return Nr(this,i.MEDIA_SUBTITLES_LIST)}set mediaSubtitlesList(e){Pr(this,i.MEDIA_SUBTITLES_LIST,e)}get mediaSubtitlesShowing(){return Nr(this,i.MEDIA_SUBTITLES_SHOWING)}set mediaSubtitlesShowing(e){Pr(this,i.MEDIA_SUBTITLES_SHOWING,e)}handleClick(){this.dispatchEvent(new v.CustomEvent(e.MEDIA_TOGGLE_SUBTITLES_REQUEST,{composed:!0,bubbles:!0}))}};Mr.getSlotTemplateHTML=kr,Mr.getTooltipContentHTML=Ar;var Nr=(e,t)=>{let n=e.getAttribute(t);return n?Mt(n):[]},Pr=(e,t,n)=>{if(!n?.length){e.removeAttribute(t);return}let r=Ft(n);e.getAttribute(t)!==r&&e.setAttribute(t,r)};v.customElements.get(`media-captions-button`)||v.customElements.define(`media-captions-button`,Mr);var Fr=`<svg aria-hidden="true" viewBox="0 0 24 24"><g><path class="cast_caf_icon_arch0" d="M1,18 L1,21 L4,21 C4,19.3 2.66,18 1,18 L1,18 Z"/><path class="cast_caf_icon_arch1" d="M1,14 L1,16 C3.76,16 6,18.2 6,21 L8,21 C8,17.13 4.87,14 1,14 L1,14 Z"/><path class="cast_caf_icon_arch2" d="M1,10 L1,12 C5.97,12 10,16.0 10,21 L12,21 C12,14.92 7.07,10 1,10 L1,10 Z"/><path class="cast_caf_icon_box" d="M21,3 L3,3 C1.9,3 1,3.9 1,5 L1,8 L3,8 L3,5 L21,5 L21,19 L14,19 L14,21 L21,21 C22.1,21 23,20.1 23,19 L23,5 C23,3.9 22.1,3 21,3 L21,3 Z"/></g></svg>`,Ir=`<svg aria-hidden="true" viewBox="0 0 24 24"><g><path class="cast_caf_icon_arch0" d="M1,18 L1,21 L4,21 C4,19.3 2.66,18 1,18 L1,18 Z"/><path class="cast_caf_icon_arch1" d="M1,14 L1,16 C3.76,16 6,18.2 6,21 L8,21 C8,17.13 4.87,14 1,14 L1,14 Z"/><path class="cast_caf_icon_arch2" d="M1,10 L1,12 C5.97,12 10,16.0 10,21 L12,21 C12,14.92 7.07,10 1,10 L1,10 Z"/><path class="cast_caf_icon_box" d="M21,3 L3,3 C1.9,3 1,3.9 1,5 L1,8 L3,8 L3,5 L21,5 L21,19 L14,19 L14,21 L21,21 C22.1,21 23,20.1 23,19 L23,5 C23,3.9 22.1,3 21,3 L21,3 Z"/><path class="cast_caf_icon_boxfill" d="M5,7 L5,8.63 C8,8.6 13.37,14 13.37,17 L19,17 L19,7 Z"/></g></svg>`;function Lr(e){return`
    <style>
      :host([${i.MEDIA_IS_CASTING}]) slot[name=icon] slot:not([name=exit]) {
        display: none !important;
      }

      
      :host(:not([${i.MEDIA_IS_CASTING}])) slot[name=icon] slot:not([name=enter]) {
        display: none !important;
      }

      :host([${i.MEDIA_IS_CASTING}]) slot[name=tooltip-enter],
      :host(:not([${i.MEDIA_IS_CASTING}])) slot[name=tooltip-exit] {
        display: none;
      }
    </style>

    <slot name="icon">
      <slot name="enter">${Fr}</slot>
      <slot name="exit">${Ir}</slot>
    </slot>
  `}function Rr(){return`
    <slot name="tooltip-enter">${_(`Start casting`)}</slot>
    <slot name="tooltip-exit">${_(`Stop casting`)}</slot>
  `}var zr=e=>{let t=e.mediaIsCasting?_(`stop casting`):_(`start casting`);e.setAttribute(`aria-label`,t)},Br=class extends H{static get observedAttributes(){return[...super.observedAttributes,i.MEDIA_IS_CASTING,i.MEDIA_CAST_UNAVAILABLE]}connectedCallback(){super.connectedCallback(),zr(this)}attributeChangedCallback(e,t,n){super.attributeChangedCallback(e,t,n),e===i.MEDIA_IS_CASTING&&zr(this)}get mediaIsCasting(){return w(this,i.MEDIA_IS_CASTING)}set mediaIsCasting(e){T(this,i.MEDIA_IS_CASTING,e)}get mediaCastUnavailable(){return E(this,i.MEDIA_CAST_UNAVAILABLE)}set mediaCastUnavailable(e){D(this,i.MEDIA_CAST_UNAVAILABLE,e)}handleClick(){let t=this.mediaIsCasting?e.MEDIA_EXIT_CAST_REQUEST:e.MEDIA_ENTER_CAST_REQUEST;this.dispatchEvent(new v.CustomEvent(t,{composed:!0,bubbles:!0}))}};Br.getSlotTemplateHTML=Lr,Br.getTooltipContentHTML=Rr,v.customElements.get(`media-cast-button`)||v.customElements.define(`media-cast-button`,Br);var Vr=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},Hr=(e,t,n)=>(Vr(e,t,`read from private field`),n?n.call(e):t.get(e)),Ur=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},Wr=(e,t,n,r)=>(Vr(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),Gr=(e,t,n)=>(Vr(e,t,`access private method`),n),Kr,qr,Jr,Yr,Xr,Zr,Qr,$r,ei,ti,ni,ri,ii,ai,oi;function si(e){return`
    <style>
      :host {
        font: var(--media-font,
          var(--media-font-weight, normal)
          var(--media-font-size, 14px) /
          var(--media-text-content-height, var(--media-control-height, 24px))
          var(--media-font-family, helvetica neue, segoe ui, roboto, arial, sans-serif));
        color: var(--media-text-color, var(--media-primary-color, rgb(238 238 238)));
        display: var(--media-dialog-display, inline-flex);
        justify-content: center;
        align-items: center;
        
        transition-behavior: allow-discrete;
        visibility: hidden;
        opacity: 0;
        transform: translateY(2px) scale(.99);
        pointer-events: none;
      }

      :host([open]) {
        transition: display .2s, visibility 0s, opacity .2s ease-out, transform .15s ease-out;
        visibility: visible;
        opacity: 1;
        transform: translateY(0) scale(1);
        pointer-events: auto;
      }

      #content {
        display: flex;
        position: relative;
        box-sizing: border-box;
        width: min(320px, 100%);
        word-wrap: break-word;
        max-height: 100%;
        overflow: auto;
        text-align: center;
        line-height: 1.4;
      }
    </style>
    ${this.getSlotTemplateHTML(e)}
  `}function ci(e){return`
    <slot id="content"></slot>
  `}var li={OPEN:`open`,ANCHOR:`anchor`},ui=class extends v.HTMLElement{constructor(){super(),Ur(this,Yr),Ur(this,Zr),Ur(this,$r),Ur(this,ti),Ur(this,ri),Ur(this,ai),Ur(this,Kr,!1),Ur(this,qr,null),Ur(this,Jr,null)}static get observedAttributes(){return[li.OPEN,li.ANCHOR]}get open(){return w(this,li.OPEN)}set open(e){T(this,li.OPEN,e)}handleEvent(e){switch(e.type){case`invoke`:Gr(this,ti,ni).call(this,e);break;case`focusout`:Gr(this,ri,ii).call(this,e);break;case`keydown`:Gr(this,ai,oi).call(this,e)}}connectedCallback(){Gr(this,Yr,Xr).call(this),this.role||=`dialog`,this.addEventListener(`invoke`,this),this.addEventListener(`focusout`,this),this.addEventListener(`keydown`,this)}disconnectedCallback(){this.removeEventListener(`invoke`,this),this.removeEventListener(`focusout`,this),this.removeEventListener(`keydown`,this)}attributeChangedCallback(e,t,n){Gr(this,Yr,Xr).call(this),e===li.OPEN&&n!==t&&(this.open?Gr(this,Zr,Qr).call(this):Gr(this,$r,ei).call(this))}focus(){Wr(this,qr,Me());let e=!this.dispatchEvent(new Event(`focus`,{composed:!0,cancelable:!0})),t=!this.dispatchEvent(new Event(`focusin`,{composed:!0,bubbles:!0,cancelable:!0}));e||t||this.querySelector(`[autofocus], [tabindex]:not([tabindex="-1"]), [role="menu"]`)?.focus()}get keysUsed(){return[`Escape`,`Tab`]}};Kr=new WeakMap,qr=new WeakMap,Jr=new WeakMap,Yr=new WeakSet,Xr=function(){if(!Hr(this,Kr)&&(Wr(this,Kr,!0),!this.shadowRoot)){this.attachShadow(this.constructor.shadowRootOptions);let e=b(this.attributes);this.shadowRoot.innerHTML=this.constructor.getTemplateHTML(e),queueMicrotask(()=>{let{style:e}=x(this.shadowRoot,`:host`);e.setProperty(`transition`,`display .15s, visibility .15s, opacity .15s ease-in, transform .15s ease-in`)})}},Zr=new WeakSet,Qr=function(){var e;(e=Hr(this,Jr))==null||e.setAttribute(`aria-expanded`,`true`),this.dispatchEvent(new Event(`open`,{composed:!0,bubbles:!0})),this.addEventListener(`transitionend`,()=>this.focus(),{once:!0})},$r=new WeakSet,ei=function(){var e;(e=Hr(this,Jr))==null||e.setAttribute(`aria-expanded`,`false`),this.dispatchEvent(new Event(`close`,{composed:!0,bubbles:!0}))},ti=new WeakSet,ni=function(e){Wr(this,Jr,e.relatedTarget),Ae(this,e.relatedTarget)||(this.open=!this.open)},ri=new WeakSet,ii=function(e){var t;Ae(this,e.relatedTarget)||((t=Hr(this,qr))==null||t.focus(),Hr(this,Jr)&&Hr(this,Jr)!==e.relatedTarget&&this.open&&(this.open=!1))},ai=new WeakSet,oi=function(e){var t,n,r,i,a;let{key:o,ctrlKey:s,altKey:c,metaKey:l}=e;s||c||l||this.keysUsed.includes(o)&&(e.preventDefault(),e.stopPropagation(),o===`Tab`?(e.shiftKey?(n=(t=this.previousElementSibling)?.focus)==null||n.call(t):(i=(r=this.nextElementSibling)?.focus)==null||i.call(r),this.blur()):o===`Escape`&&((a=Hr(this,qr))==null||a.focus(),this.open=!1))},ui.shadowRootOptions={mode:`open`},ui.getTemplateHTML=si,ui.getSlotTemplateHTML=ci,v.customElements.get(`media-chrome-dialog`)||v.customElements.define(`media-chrome-dialog`,ui);var di=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},U=(e,t,n)=>(di(e,t,`read from private field`),n?n.call(e):t.get(e)),W=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},fi=(e,t,n,r)=>(di(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),G=(e,t,n)=>(di(e,t,`access private method`),n),pi,mi,hi,gi,K,_i,vi,yi,bi,xi,Si,Ci,wi,Ti,Ei,Di,Oi,ki,Ai,ji,Mi,Ni,Pi,Fi,Ii;function Li(e){return`
    <style>
      :host {
        --_focus-box-shadow: var(--media-focus-box-shadow, inset 0 0 0 2px rgb(27 127 204 / .9));
        --_media-range-padding: var(--media-range-padding, var(--media-control-padding, 10px));

        box-shadow: var(--_focus-visible-box-shadow, none);
        background: var(--media-control-background, var(--media-secondary-color, rgb(20 20 30 / .7)));
        height: calc(var(--media-control-height, 24px) + 2 * var(--_media-range-padding));
        display: inline-flex;
        align-items: center;
        
        vertical-align: middle;
        box-sizing: border-box;
        position: relative;
        width: 100px;
        transition: background .15s linear;
        cursor: var(--media-cursor, pointer);
        pointer-events: auto;
        touch-action: none; 
      }

      
      input[type=range]:focus {
        outline: 0;
      }
      input[type=range]:focus::-webkit-slider-runnable-track {
        outline: 0;
      }

      :host(:hover) {
        background: var(--media-control-hover-background, rgb(50 50 70 / .7));
      }

      #leftgap {
        padding-left: var(--media-range-padding-left, var(--_media-range-padding));
      }

      #rightgap {
        padding-right: var(--media-range-padding-right, var(--_media-range-padding));
      }

      #startpoint,
      #endpoint {
        position: absolute;
      }

      #endpoint {
        right: 0;
      }

      #container {
        
        width: var(--media-range-track-width, 100%);
        transform: translate(var(--media-range-track-translate-x, 0px), var(--media-range-track-translate-y, 0px));
        position: relative;
        height: 100%;
        display: flex;
        align-items: center;
        min-width: 40px;
      }

      #range {
        
        display: var(--media-time-range-hover-display, block);
        bottom: var(--media-time-range-hover-bottom, 0);
        height: var(--media-time-range-hover-height, max(100% , 25px));
        width: 100%;
        position: absolute;
        cursor: var(--media-cursor, pointer);

        -webkit-appearance: none; 
        -webkit-tap-highlight-color: transparent;
        background: transparent; 
        margin: 0;
        z-index: 1;
      }

      @media (hover: hover) {
        #range {
          bottom: var(--media-time-range-hover-bottom, 0);
          height: var(--media-time-range-hover-height, max(100%, 20px));
        }
      }

      
      
      #range::-webkit-slider-thumb {
        -webkit-appearance: none;
        background: transparent;
        width: .1px;
        height: .1px;
      }

      
      #range::-moz-range-thumb {
        background: transparent;
        border: transparent;
        width: .1px;
        height: .1px;
      }

      #appearance {
        height: var(--media-range-track-height, 4px);
        display: flex;
        flex-direction: column;
        justify-content: center;
        width: 100%;
        position: absolute;
        
        will-change: transform;
      }

      #track {
        background: var(--media-range-track-background, rgb(255 255 255 / .2));
        border-radius: var(--media-range-track-border-radius, 1px);
        border: var(--media-range-track-border, none);
        outline: var(--media-range-track-outline);
        outline-offset: var(--media-range-track-outline-offset);
        backdrop-filter: var(--media-range-track-backdrop-filter);
        -webkit-backdrop-filter: var(--media-range-track-backdrop-filter);
        box-shadow: var(--media-range-track-box-shadow, none);
        position: absolute;
        width: 100%;
        height: 100%;
        overflow: hidden;
      }

      #progress,
      #pointer {
        position: absolute;
        height: 100%;
        will-change: width;
      }

      #progress {
        background: var(--media-range-bar-color, var(--media-primary-color, rgb(238 238 238)));
        transition: var(--media-range-track-transition);
      }

      #pointer {
        background: var(--media-range-track-pointer-background);
        border-right: var(--media-range-track-pointer-border-right);
        transition: visibility .25s, opacity .25s;
        visibility: hidden;
        opacity: 0;
      }

      @media (hover: hover) {
        :host(:hover) #pointer {
          transition: visibility .5s, opacity .5s;
          visibility: visible;
          opacity: 1;
        }
      }

      #thumb,
      ::slotted([slot=thumb]) {
        width: var(--media-range-thumb-width, 10px);
        height: var(--media-range-thumb-height, 10px);
        transition: var(--media-range-thumb-transition);
        transform: var(--media-range-thumb-transform, none);
        opacity: var(--media-range-thumb-opacity, 1);
        translate: -50%;
        position: absolute;
        left: 0;
        cursor: var(--media-cursor, pointer);
      }

      #thumb {
        border-radius: var(--media-range-thumb-border-radius, 10px);
        background: var(--media-range-thumb-background, var(--media-primary-color, rgb(238 238 238)));
        box-shadow: var(--media-range-thumb-box-shadow, 1px 1px 1px transparent);
        border: var(--media-range-thumb-border, none);
      }

      :host([disabled]) #thumb {
        background-color: #777;
      }

      .segments #appearance {
        height: var(--media-range-segment-hover-height, 7px);
      }

      #track {
        clip-path: url(#segments-clipping);
      }

      #segments {
        --segments-gap: var(--media-range-segments-gap, 2px);
        position: absolute;
        width: 100%;
        height: 100%;
      }

      #segments-clipping {
        transform: translateX(calc(var(--segments-gap) / 2));
      }

      #segments-clipping:empty {
        display: none;
      }

      #segments-clipping rect {
        height: var(--media-range-track-height, 4px);
        y: calc((var(--media-range-segment-hover-height, 7px) - var(--media-range-track-height, 4px)) / 2);
        transition: var(--media-range-segment-transition, transform .1s ease-in-out);
        transform: var(--media-range-segment-transform, scaleY(1));
        transform-origin: center;
      }

      /* Visible label for accessibility - positioned off-screen but technically visible (Firefox requires visible labels) */
      #range-label {
        position: absolute;
        left: -10000px;
        background: var(--media-control-background, var(--media-secondary-color, rgb(20 20 30 / .7)));
        pointer-events: none;
      }
    </style>
    <div id="leftgap"></div>
    <div id="container">
      <div id="startpoint"></div>
      <div id="endpoint"></div>
      <div id="appearance">
        <div id="track" part="track">
          <div id="pointer"></div>
          <div id="progress" part="progress"></div>
        </div>
        <slot name="thumb">
          <div id="thumb" part="thumb"></div>
        </slot>
        <svg id="segments" aria-hidden="true"><clipPath id="segments-clipping"></clipPath></svg>
      </div>
        <input id="range" type="range" min="0" max="1" step="any" value="0">
        <label for="range" id="range-label"></label>

      ${this.getContainerTemplateHTML(e)}
    </div>
    <div id="rightgap"></div>
  `}function Ri(e){return``}var zi=class extends v.HTMLElement{constructor(){if(super(),W(this,xi),W(this,Ci),W(this,Ti),W(this,Di),W(this,ki),W(this,ji),W(this,Ni),W(this,Fi),W(this,pi,void 0),W(this,mi,void 0),W(this,hi,void 0),W(this,gi,void 0),W(this,K,{}),W(this,_i,[]),W(this,vi,()=>{if(this.range.matches(`:focus-visible`)){let{style:e}=x(this.shadowRoot,`:host`);e.setProperty(`--_focus-visible-box-shadow`,`var(--_focus-box-shadow)`)}}),W(this,yi,()=>{let{style:e}=x(this.shadowRoot,`:host`);e.removeProperty(`--_focus-visible-box-shadow`)}),W(this,bi,()=>{let e=this.shadowRoot.querySelector(`#segments-clipping`);e&&e.parentNode.append(e)}),!this.shadowRoot){this.attachShadow(this.constructor.shadowRootOptions);let e=b(this.attributes),t=this.constructor.getTemplateHTML(e);this.shadowRoot.setHTMLUnsafe?this.shadowRoot.setHTMLUnsafe(t):this.shadowRoot.innerHTML=t}this.container=this.shadowRoot.querySelector(`#container`),fi(this,hi,this.shadowRoot.querySelector(`#startpoint`)),fi(this,gi,this.shadowRoot.querySelector(`#endpoint`)),this.range=this.shadowRoot.querySelector(`#range`),this.appearance=this.shadowRoot.querySelector(`#appearance`)}static get observedAttributes(){return[`disabled`,`aria-disabled`,t.MEDIA_CONTROLLER]}attributeChangedCallback(e,n,r){var i,a,o,s;e===t.MEDIA_CONTROLLER?(n&&((a=(i=U(this,pi))?.unassociateElement)==null||a.call(i,this),fi(this,pi,null)),r&&this.isConnected&&(fi(this,pi,this.getRootNode()?.getElementById(r)),(s=(o=U(this,pi))?.associateElement)==null||s.call(o,this))):(e===`disabled`||e===`aria-disabled`&&n!==r)&&(r==null?(this.range.removeAttribute(e),G(this,Ci,wi).call(this)):(this.range.setAttribute(e,r),G(this,Ti,Ei).call(this)))}connectedCallback(){var e,n;let{style:r}=x(this.shadowRoot,`:host`);r.setProperty(`display`,`var(--media-control-display, var(--${this.localName}-display, inline-flex))`),U(this,K).pointer=x(this.shadowRoot,`#pointer`),U(this,K).progress=x(this.shadowRoot,`#progress`),U(this,K).thumb=x(this.shadowRoot,`#thumb, ::slotted([slot="thumb"])`),U(this,K).activeSegment=x(this.shadowRoot,`#segments-clipping rect:nth-child(0)`);let i=this.getAttribute(t.MEDIA_CONTROLLER);i&&(fi(this,pi,this.getRootNode()?.getElementById(i)),(n=(e=U(this,pi))?.associateElement)==null||n.call(e,this)),this.updateBar(),this.shadowRoot.addEventListener(`focusin`,U(this,vi)),this.shadowRoot.addEventListener(`focusout`,U(this,yi)),G(this,Ci,wi).call(this),Ce(this.container,U(this,bi))}disconnectedCallback(){var e,t;G(this,Ti,Ei).call(this),(t=(e=U(this,pi))?.unassociateElement)==null||t.call(e,this),fi(this,pi,null),this.shadowRoot.removeEventListener(`focusin`,U(this,vi)),this.shadowRoot.removeEventListener(`focusout`,U(this,yi)),we(this.container,U(this,bi))}updatePointerBar(e){var t;(t=U(this,K).pointer)==null||t.style.setProperty(`width`,`${this.getPointerRatio(e)*100}%`)}updateBar(){var e,t;let n=this.range.valueAsNumber*100;(e=U(this,K).progress)==null||e.style.setProperty(`width`,`${n}%`),(t=U(this,K).thumb)==null||t.style.setProperty(`left`,`${n}%`)}updateSegments(e){let t=this.shadowRoot.querySelector(`#segments-clipping`);if(t.textContent=``,this.container.classList.toggle(`segments`,!!e?.length),!e?.length)return;let n=[...new Set([+this.range.min,...e.flatMap(e=>[e.start,e.end]),+this.range.max])];fi(this,_i,[...n]);let r=n.pop();for(let[e,i]of n.entries()){let[a,o]=[e===0,e===n.length-1],s=a?`calc(var(--segments-gap) / -1)`:`${i*100}%`,c=`calc(${((o?r:n[e+1])-i)*100}%${a||o?``:` - var(--segments-gap)`})`,l=y.createElementNS(`http://www.w3.org/2000/svg`,`rect`),u=Le(this.shadowRoot,`#segments-clipping rect:nth-child(${e+1})`);u.style.setProperty(`x`,s),u.style.setProperty(`width`,c),t.append(l)}}getPointerRatio(e){return Fe(e.clientX,e.clientY,U(this,hi).getBoundingClientRect(),U(this,gi).getBoundingClientRect())}get dragging(){return this.hasAttribute(`dragging`)}handleEvent(e){switch(e.type){case`pointermove`:G(this,Fi,Ii).call(this,e);break;case`input`:this.updateBar();break;case`pointerenter`:G(this,ki,Ai).call(this,e);break;case`pointerdown`:G(this,Di,Oi).call(this,e);break;case`pointerup`:G(this,ji,Mi).call(this);break;case`pointerleave`:G(this,Ni,Pi).call(this)}}get keysUsed(){return[`ArrowUp`,`ArrowRight`,`ArrowDown`,`ArrowLeft`]}};pi=new WeakMap,mi=new WeakMap,hi=new WeakMap,gi=new WeakMap,K=new WeakMap,_i=new WeakMap,vi=new WeakMap,yi=new WeakMap,bi=new WeakMap,xi=new WeakSet,Si=function(e){let t=U(this,K).activeSegment;if(!t)return;let n=this.getPointerRatio(e),r=`#segments-clipping rect:nth-child(${U(this,_i).findIndex((e,t,r)=>{let i=r[t+1];return i!=null&&n>=e&&n<=i})+1})`;(t.selectorText!=r||!t.style.transform)&&(t.selectorText=r,t.style.setProperty(`transform`,`var(--media-range-segment-hover-transform, scaleY(2))`))},Ci=new WeakSet,wi=function(){this.hasAttribute(`disabled`)||!this.isConnected||(this.addEventListener(`input`,this),this.addEventListener(`pointerdown`,this),this.addEventListener(`pointerenter`,this))},Ti=new WeakSet,Ei=function(){var e,t;this.removeEventListener(`input`,this),this.removeEventListener(`pointerdown`,this),this.removeEventListener(`pointerenter`,this),this.removeEventListener(`pointerleave`,this),(e=v.window)==null||e.removeEventListener(`pointerup`,this),(t=v.window)==null||t.removeEventListener(`pointermove`,this)},Di=new WeakSet,Oi=function(e){var t;fi(this,mi,e.composedPath().includes(this.range)),(t=v.window)==null||t.addEventListener(`pointerup`,this,{once:!0})},ki=new WeakSet,Ai=function(e){var t;e.pointerType!==`mouse`&&G(this,Di,Oi).call(this,e),this.addEventListener(`pointerleave`,this,{once:!0}),(t=v.window)==null||t.addEventListener(`pointermove`,this)},ji=new WeakSet,Mi=function(){var e;(e=v.window)==null||e.removeEventListener(`pointerup`,this),this.toggleAttribute(`dragging`,!1),this.range.disabled=this.hasAttribute(`disabled`)},Ni=new WeakSet,Pi=function(){var e,t;this.removeEventListener(`pointerleave`,this),(e=v.window)==null||e.removeEventListener(`pointermove`,this),this.toggleAttribute(`dragging`,!1),this.range.disabled=this.hasAttribute(`disabled`),(t=U(this,K).activeSegment)==null||t.style.removeProperty(`transform`)},Fi=new WeakSet,Ii=function(e){(e.pointerType!==`pen`||e.buttons!==0)&&(this.toggleAttribute(`dragging`,e.buttons===1||e.pointerType!==`mouse`),this.updatePointerBar(e),G(this,xi,Si).call(this,e),this.dragging&&(e.pointerType!==`mouse`||!U(this,mi))&&(this.range.disabled=!0,this.range.valueAsNumber=this.getPointerRatio(e),this.range.dispatchEvent(new Event(`input`,{bubbles:!0,composed:!0}))))},zi.shadowRootOptions={mode:`open`},zi.getTemplateHTML=Li,zi.getContainerTemplateHTML=Ri,v.customElements.get(`media-chrome-range`)||v.customElements.define(`media-chrome-range`,zi);var Bi=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},Vi=(e,t,n)=>(Bi(e,t,`read from private field`),n?n.call(e):t.get(e)),Hi=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},Ui=(e,t,n,r)=>(Bi(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),Wi;function Gi(e){return`
    <style>
      :host {
        
        box-sizing: border-box;
        display: var(--media-control-display, var(--media-control-bar-display, inline-flex));
        color: var(--media-text-color, var(--media-primary-color, rgb(238 238 238)));
        --media-loading-indicator-icon-height: 44px;
      }

      ::slotted(media-time-range),
      ::slotted(media-volume-range) {
        min-height: 100%;
      }

      ::slotted(media-time-range),
      ::slotted(media-clip-selector) {
        flex-grow: 1;
      }

      ::slotted([role="menu"]) {
        position: absolute;
      }
    </style>

    <slot></slot>
  `}var Ki=class extends v.HTMLElement{constructor(){if(super(),Hi(this,Wi,void 0),!this.shadowRoot){this.attachShadow(this.constructor.shadowRootOptions);let e=b(this.attributes);this.shadowRoot.innerHTML=this.constructor.getTemplateHTML(e)}}static get observedAttributes(){return[t.MEDIA_CONTROLLER]}attributeChangedCallback(e,n,r){var i,a,o,s;e===t.MEDIA_CONTROLLER&&(n&&((a=(i=Vi(this,Wi))?.unassociateElement)==null||a.call(i,this),Ui(this,Wi,null)),r&&this.isConnected&&(Ui(this,Wi,this.getRootNode()?.getElementById(r)),(s=(o=Vi(this,Wi))?.associateElement)==null||s.call(o,this)))}connectedCallback(){var e,n;let r=this.getAttribute(t.MEDIA_CONTROLLER);r&&(Ui(this,Wi,this.getRootNode()?.getElementById(r)),(n=(e=Vi(this,Wi))?.associateElement)==null||n.call(e,this))}disconnectedCallback(){var e,t;(t=(e=Vi(this,Wi))?.unassociateElement)==null||t.call(e,this),Ui(this,Wi,null)}};Wi=new WeakMap,Ki.shadowRootOptions={mode:`open`},Ki.getTemplateHTML=Gi,v.customElements.get(`media-control-bar`)||v.customElements.define(`media-control-bar`,Ki);var qi=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},Ji=(e,t,n)=>(qi(e,t,`read from private field`),n?n.call(e):t.get(e)),Yi=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},Xi=(e,t,n,r)=>(qi(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),Zi;function Qi(e,t={}){return`
    <style>
      :host {
        font: var(--media-font,
          var(--media-font-weight, normal)
          var(--media-font-size, 14px) /
          var(--media-text-content-height, var(--media-control-height, 24px))
          var(--media-font-family, helvetica neue, segoe ui, roboto, arial, sans-serif));
        color: var(--media-text-color, var(--media-primary-color, rgb(238 238 238)));
        background: var(--media-text-background, var(--media-control-background, var(--media-secondary-color, rgb(20 20 30 / .7))));
        padding: var(--media-control-padding, 10px);
        display: inline-flex;
        justify-content: center;
        align-items: center;
        vertical-align: middle;
        box-sizing: border-box;
        text-align: center;
        pointer-events: auto;
      }

      
      :host(:focus-visible) {
        box-shadow: var(--media-focus-box-shadow, inset 0 0 0 2px rgb(27 127 204 / .9));
        outline: 0;
      }

      
      :host(:where(:focus)) {
        box-shadow: none;
        outline: 0;
      }
    </style>

    ${this.getSlotTemplateHTML(e,t)}
  `}function $i(e,t){return`
    <slot></slot>
  `}var ea=class extends v.HTMLElement{constructor(){if(super(),Yi(this,Zi,void 0),!this.shadowRoot){this.attachShadow(this.constructor.shadowRootOptions);let e=b(this.attributes);this.shadowRoot.innerHTML=this.constructor.getTemplateHTML(e)}}static get observedAttributes(){return[t.MEDIA_CONTROLLER]}attributeChangedCallback(e,n,r){var i,a,o,s;e===t.MEDIA_CONTROLLER&&(n&&((a=(i=Ji(this,Zi))?.unassociateElement)==null||a.call(i,this),Xi(this,Zi,null)),r&&this.isConnected&&(Xi(this,Zi,this.getRootNode()?.getElementById(r)),(s=(o=Ji(this,Zi))?.associateElement)==null||s.call(o,this)))}connectedCallback(){var e,n;let{style:r}=x(this.shadowRoot,`:host`);r.setProperty(`display`,`var(--media-control-display, var(--${this.localName}-display, inline-flex))`);let i=this.getAttribute(t.MEDIA_CONTROLLER);i&&(Xi(this,Zi,this.getRootNode()?.getElementById(i)),(n=(e=Ji(this,Zi))?.associateElement)==null||n.call(e,this))}disconnectedCallback(){var e,t;(t=(e=Ji(this,Zi))?.unassociateElement)==null||t.call(e,this),Xi(this,Zi,null)}};Zi=new WeakMap,ea.shadowRootOptions={mode:`open`},ea.getTemplateHTML=Qi,ea.getSlotTemplateHTML=$i,v.customElements.get(`media-text-display`)||v.customElements.define(`media-text-display`,ea);var ta=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},na=(e,t,n)=>(ta(e,t,`read from private field`),n?n.call(e):t.get(e)),ra=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},ia=(e,t,n,r)=>(ta(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),aa;function oa(e,t){return`
    <slot>${de(t.mediaDuration)}</slot>
  `}var sa=class extends ea{constructor(){super(),ra(this,aa,void 0),ia(this,aa,this.shadowRoot.querySelector(`slot`)),na(this,aa).textContent=de(this.mediaDuration??0)}static get observedAttributes(){return[...super.observedAttributes,i.MEDIA_DURATION]}attributeChangedCallback(e,t,n){e===i.MEDIA_DURATION&&(na(this,aa).textContent=de(+n)),super.attributeChangedCallback(e,t,n)}get mediaDuration(){return S(this,i.MEDIA_DURATION)}set mediaDuration(e){C(this,i.MEDIA_DURATION,e)}};aa=new WeakMap,sa.getSlotTemplateHTML=oa,v.customElements.get(`media-duration-display`)||v.customElements.define(`media-duration-display`,sa);var ca={2:_(`Network Error`),3:_(`Decode Error`),4:_(`Source Not Supported`),5:_(`Encryption Error`)},la={2:_(`A network error caused the media download to fail.`),3:_(`A media error caused playback to be aborted. The media could be corrupt or your browser does not support this format.`),4:_(`An unsupported error occurred. The server or network failed, or your browser does not support this format.`),5:_(`The media is encrypted and there are no keys to decrypt it.`)},ua=e=>e.code===1?null:{title:ca[e.code]??`Error ${e.code}`,message:la[e.code]??e.message},da=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},fa=(e,t,n)=>(da(e,t,`read from private field`),n?n.call(e):t.get(e)),pa=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},ma=(e,t,n,r)=>(da(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),ha;function ga(e){return`
    <style>
      :host {
        background: rgb(20 20 30 / .8);
      }

      #content {
        display: block;
        padding: 1.2em 1.5em;
      }

      h3,
      p {
        margin-block: 0 .3em;
      }
    </style>
    <slot name="error-${e.mediaerrorcode}" id="content">
      ${va({code:+e.mediaerrorcode,message:e.mediaerrormessage})}
    </slot>
  `}function _a(e){return e.code&&ua(e)!==null}function va(e){let{title:t,message:n}=ua(e)??{},r=``;return t&&(r+=`<slot name="error-${e.code}-title"><h3>${t}</h3></slot>`),n&&(r+=`<slot name="error-${e.code}-message"><p>${n}</p></slot>`),r}var ya=[i.MEDIA_ERROR_CODE,i.MEDIA_ERROR_MESSAGE],ba=class extends ui{constructor(){super(...arguments),pa(this,ha,null)}static get observedAttributes(){return[...super.observedAttributes,...ya]}formatErrorMessage(e){return this.constructor.formatErrorMessage(e)}attributeChangedCallback(e,t,n){if(super.attributeChangedCallback(e,t,n),!ya.includes(e))return;let r=this.mediaError??{code:this.mediaErrorCode,message:this.mediaErrorMessage};if(this.open=_a(r),this.open&&(this.shadowRoot.querySelector(`slot`).name=`error-${this.mediaErrorCode}`,this.shadowRoot.querySelector(`#content`).innerHTML=this.formatErrorMessage(r),!this.hasAttribute(`aria-label`))){let{title:e}=ua(r);e&&this.setAttribute(`aria-label`,e)}}get mediaError(){return fa(this,ha)}set mediaError(e){ma(this,ha,e)}get mediaErrorCode(){return S(this,`mediaerrorcode`)}set mediaErrorCode(e){C(this,`mediaerrorcode`,e)}get mediaErrorMessage(){return E(this,`mediaerrormessage`)}set mediaErrorMessage(e){D(this,`mediaerrormessage`,e)}};ha=new WeakMap,ba.getSlotTemplateHTML=ga,ba.formatErrorMessage=va,v.customElements.get(`media-error-dialog`)||v.customElements.define(`media-error-dialog`,ba);var xa=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},Sa=(e,t,n)=>(xa(e,t,`read from private field`),n?n.call(e):t.get(e)),Ca=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},wa,Ta;function Ea(e){return`
    <style>
      :host {
        position: fixed;
        top: 0;
        left: 0;
        z-index: 9999;
        background: rgb(20 20 30 / .8);
        backdrop-filter: blur(10px);
      }

      #content {
        display: block;
        width: clamp(400px, 40vw, 700px);
        max-width: 90vw;
        text-align: left;
      }

      h2 {
        margin: 0 0 1.5rem 0;
        font-size: 1.5rem;
        font-weight: 500;
        text-align: center;
      }

      .shortcuts-table {
        width: 100%;
        border-collapse: collapse;
      }

      .shortcuts-table tr {
        border-bottom: 1px solid rgba(255, 255, 255, 0.1);
      }

      .shortcuts-table tr:last-child {
        border-bottom: none;
      }

      .shortcuts-table td {
        padding: 0.75rem 0.5rem;
      }

      .shortcuts-table td:first-child {
        text-align: right;
        padding-right: 1rem;
        width: 40%;
        min-width: 120px;
      }

      .shortcuts-table td:last-child {
        padding-left: 1rem;
      }

      .key {
        display: inline-block;
        background: rgba(255, 255, 255, 0.15);
        border: 1px solid rgba(255, 255, 255, 0.2);
        border-radius: 4px;
        padding: 0.25rem 0.5rem;
        font-family: 'Courier New', monospace;
        font-size: 0.9rem;
        font-weight: 500;
        min-width: 1.5rem;
        text-align: center;
        margin: 0 0.2rem;
      }

      .description {
        color: rgba(255, 255, 255, 0.9);
        font-size: 0.95rem;
      }

      .key-combo {
        display: flex;
        align-items: center;
        justify-content: flex-end;
        gap: 0.3rem;
      }

      .key-separator {
        color: rgba(255, 255, 255, 0.5);
        font-size: 0.9rem;
      }
    </style>
    <slot id="content">
      ${Da()}
    </slot>
  `}function Da(){return`
    <h2>Keyboard Shortcuts</h2>
    <table class="shortcuts-table">${[{keys:[`Space`,`k`],description:`Toggle Playback`},{keys:[`m`],description:`Toggle mute`},{keys:[`f`],description:`Toggle fullscreen`},{keys:[`c`],description:`Toggle captions or subtitles, if available`},{keys:[`p`],description:`Toggle Picture in Picture`},{keys:[`←`,`j`],description:`Seek back 10s`},{keys:[`→`,`l`],description:`Seek forward 10s`},{keys:[`↑`],description:`Turn volume up`},{keys:[`↓`],description:`Turn volume down`},{keys:[`< (SHIFT+,)`],description:`Decrease playback rate`},{keys:[`> (SHIFT+.)`],description:`Increase playback rate`}].map(({keys:e,description:t})=>`
      <tr>
        <td>
          <div class="key-combo">${e.map((e,t)=>t>0?`<span class="key-separator">or</span><span class="key">${e}</span>`:`<span class="key">${e}</span>`).join(``)}</div>
        </td>
        <td class="description">${t}</td>
      </tr>
    `).join(``)}</table>
  `}var Oa=class extends ui{constructor(){super(...arguments),Ca(this,wa,e=>{if(!this.open)return;let t=this.shadowRoot?.querySelector(`#content`);if(!t)return;let n=e.composedPath(),r=n[0]===this||n.includes(this),i=n.includes(t);r&&!i&&(this.open=!1)}),Ca(this,Ta,e=>{if(!this.open)return;let t=e.shiftKey&&(e.key===`/`||e.key===`?`);(e.key===`Escape`||t)&&!e.ctrlKey&&!e.altKey&&!e.metaKey&&(this.open=!1,e.preventDefault(),e.stopPropagation())})}connectedCallback(){super.connectedCallback(),this.open&&(this.addEventListener(`click`,Sa(this,wa)),document.addEventListener(`keydown`,Sa(this,Ta)))}disconnectedCallback(){this.removeEventListener(`click`,Sa(this,wa)),document.removeEventListener(`keydown`,Sa(this,Ta))}attributeChangedCallback(e,t,n){super.attributeChangedCallback(e,t,n),e===`open`&&(this.open?(this.addEventListener(`click`,Sa(this,wa)),document.addEventListener(`keydown`,Sa(this,Ta))):(this.removeEventListener(`click`,Sa(this,wa)),document.removeEventListener(`keydown`,Sa(this,Ta))))}};wa=new WeakMap,Ta=new WeakMap,Oa.getSlotTemplateHTML=Ea,v.customElements.get(`media-keyboard-shortcuts-dialog`)||v.customElements.define(`media-keyboard-shortcuts-dialog`,Oa);var ka=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},Aa=(e,t,n)=>(ka(e,t,`read from private field`),n?n.call(e):t.get(e)),ja=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},Ma=(e,t,n,r)=>(ka(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),Na,Pa=`<svg aria-hidden="true" viewBox="0 0 26 24">
  <path d="M16 3v2.5h3.5V9H22V3h-6ZM4 9h2.5V5.5H10V3H4v6Zm15.5 9.5H16V21h6v-6h-2.5v3.5ZM6.5 15H4v6h6v-2.5H6.5V15Z"/>
</svg>`,Fa=`<svg aria-hidden="true" viewBox="0 0 26 24">
  <path d="M18.5 6.5V3H16v6h6V6.5h-3.5ZM16 21h2.5v-3.5H22V15h-6v6ZM4 17.5h3.5V21H10v-6H4v2.5Zm3.5-11H4V9h6V3H7.5v3.5Z"/>
</svg>`;function Ia(e){return`
    <style>
      :host([${i.MEDIA_IS_FULLSCREEN}]) slot[name=icon] slot:not([name=exit]) {
        display: none !important;
      }

      
      :host(:not([${i.MEDIA_IS_FULLSCREEN}])) slot[name=icon] slot:not([name=enter]) {
        display: none !important;
      }

      :host([${i.MEDIA_IS_FULLSCREEN}]) slot[name=tooltip-enter],
      :host(:not([${i.MEDIA_IS_FULLSCREEN}])) slot[name=tooltip-exit] {
        display: none;
      }
    </style>

    <slot name="icon">
      <slot name="enter">${Pa}</slot>
      <slot name="exit">${Fa}</slot>
    </slot>
  `}function La(){return`
    <slot name="tooltip-enter">${_(`Enter fullscreen mode`)}</slot>
    <slot name="tooltip-exit">${_(`Exit fullscreen mode`)}</slot>
  `}var Ra=e=>{let t=e.mediaIsFullscreen?_(`exit fullscreen mode`):_(`enter fullscreen mode`);e.setAttribute(`aria-label`,t)},za=class extends H{constructor(){super(...arguments),ja(this,Na,null)}static get observedAttributes(){return[...super.observedAttributes,i.MEDIA_IS_FULLSCREEN,i.MEDIA_FULLSCREEN_UNAVAILABLE]}connectedCallback(){super.connectedCallback(),Ra(this)}attributeChangedCallback(e,t,n){super.attributeChangedCallback(e,t,n),e===i.MEDIA_IS_FULLSCREEN&&Ra(this)}get mediaFullscreenUnavailable(){return E(this,i.MEDIA_FULLSCREEN_UNAVAILABLE)}set mediaFullscreenUnavailable(e){D(this,i.MEDIA_FULLSCREEN_UNAVAILABLE,e)}get mediaIsFullscreen(){return w(this,i.MEDIA_IS_FULLSCREEN)}set mediaIsFullscreen(e){T(this,i.MEDIA_IS_FULLSCREEN,e)}handleClick(t){Ma(this,Na,t);let n=Aa(this,Na)instanceof PointerEvent,r=this.mediaIsFullscreen?new v.CustomEvent(e.MEDIA_EXIT_FULLSCREEN_REQUEST,{composed:!0,bubbles:!0}):new v.CustomEvent(e.MEDIA_ENTER_FULLSCREEN_REQUEST,{composed:!0,bubbles:!0,detail:n});this.dispatchEvent(r)}};Na=new WeakMap,za.getSlotTemplateHTML=Ia,za.getTooltipContentHTML=La,v.customElements.get(`media-fullscreen-button`)||v.customElements.define(`media-fullscreen-button`,za);var{MEDIA_TIME_IS_LIVE:Ba,MEDIA_PAUSED:Va}=i,{MEDIA_SEEK_TO_LIVE_REQUEST:Ha,MEDIA_PLAY_REQUEST:Ua}=e,Wa=`<svg viewBox="0 0 6 12" aria-hidden="true"><circle cx="3" cy="6" r="2"></circle></svg>`;function Ga(e){return`
    <style>
      :host { --media-tooltip-display: none; }
      
      slot[name=indicator] > *,
      :host ::slotted([slot=indicator]) {
        
        min-width: auto;
        fill: var(--media-live-button-icon-color, rgb(140, 140, 140));
        color: var(--media-live-button-icon-color, rgb(140, 140, 140));
      }

      :host([${Ba}]:not([${Va}])) slot[name=indicator] > *,
      :host([${Ba}]:not([${Va}])) ::slotted([slot=indicator]) {
        fill: var(--media-live-button-indicator-color, rgb(255, 0, 0));
        color: var(--media-live-button-indicator-color, rgb(255, 0, 0));
      }

      :host([${Ba}]:not([${Va}])) {
        cursor: var(--media-cursor, not-allowed);
      }

      slot[name=text]{
        text-transform: uppercase;
      }

    </style>

    <slot name="indicator">${Wa}</slot>
    
    <slot name="spacer">&nbsp;</slot><slot name="text">${_(`live`)}</slot>
  `}var Ka=e=>{let t=e.mediaPaused||!e.mediaTimeIsLive,n=_(t?`seek to live`:`playing live`);e.setAttribute(`aria-label`,n);let r=e.shadowRoot?.querySelector(`slot[name="text"]`);r&&(r.textContent=_(`live`)),t?e.removeAttribute(`aria-disabled`):e.setAttribute(`aria-disabled`,`true`)},qa=class extends H{static get observedAttributes(){return[...super.observedAttributes,Ba,Va]}connectedCallback(){super.connectedCallback(),Ka(this)}attributeChangedCallback(e,t,n){super.attributeChangedCallback(e,t,n),Ka(this)}get mediaPaused(){return w(this,i.MEDIA_PAUSED)}set mediaPaused(e){T(this,i.MEDIA_PAUSED,e)}get mediaTimeIsLive(){return w(this,i.MEDIA_TIME_IS_LIVE)}set mediaTimeIsLive(e){T(this,i.MEDIA_TIME_IS_LIVE,e)}handleClick(){!this.mediaPaused&&this.mediaTimeIsLive||(this.dispatchEvent(new v.CustomEvent(Ha,{composed:!0,bubbles:!0})),this.hasAttribute(Va)&&this.dispatchEvent(new v.CustomEvent(Ua,{composed:!0,bubbles:!0})))}};qa.getSlotTemplateHTML=Ga,v.customElements.get(`media-live-button`)||v.customElements.define(`media-live-button`,qa);var Ja=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},Ya=(e,t,n)=>(Ja(e,t,`read from private field`),n?n.call(e):t.get(e)),Xa=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},Za=(e,t,n,r)=>(Ja(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),Qa,$a,eo={LOADING_DELAY:`loadingdelay`,NO_AUTOHIDE:`noautohide`},to=500,no=`
<svg aria-hidden="true" viewBox="0 0 100 100">
  <path d="M73,50c0-12.7-10.3-23-23-23S27,37.3,27,50 M30.9,50c0-10.5,8.5-19.1,19.1-19.1S69.1,39.5,69.1,50">
    <animateTransform
       attributeName="transform"
       attributeType="XML"
       type="rotate"
       dur="1s"
       from="0 50 50"
       to="360 50 50"
       repeatCount="indefinite" />
  </path>
</svg>
`;function ro(e){return`
    <style>
      :host {
        display: var(--media-control-display, var(--media-loading-indicator-display, inline-block));
        vertical-align: middle;
        box-sizing: border-box;
        --_loading-indicator-delay: var(--media-loading-indicator-transition-delay, ${to}ms);
      }

      #status {
        color: rgba(0,0,0,0);
        width: 0px;
        height: 0px;
      }

      :host slot[name=icon] > *,
      :host ::slotted([slot=icon]) {
        opacity: var(--media-loading-indicator-opacity, 0);
        transition: opacity 0.15s;
      }

      :host([${i.MEDIA_LOADING}]:not([${i.MEDIA_PAUSED}])) slot[name=icon] > *,
      :host([${i.MEDIA_LOADING}]:not([${i.MEDIA_PAUSED}])) ::slotted([slot=icon]) {
        opacity: var(--media-loading-indicator-opacity, 1);
        transition: opacity 0.15s var(--_loading-indicator-delay);
      }

      :host #status {
        visibility: var(--media-loading-indicator-opacity, hidden);
        transition: visibility 0.15s;
      }

      :host([${i.MEDIA_LOADING}]:not([${i.MEDIA_PAUSED}])) #status {
        visibility: var(--media-loading-indicator-opacity, visible);
        transition: visibility 0.15s var(--_loading-indicator-delay);
      }

      svg, img, ::slotted(svg), ::slotted(img) {
        width: var(--media-loading-indicator-icon-width);
        height: var(--media-loading-indicator-icon-height, 100px);
        fill: var(--media-icon-color, var(--media-primary-color, rgb(238 238 238)));
        vertical-align: middle;
      }
    </style>

    <slot name="icon">${no}</slot>
    <div id="status" role="status" aria-live="polite">${_(`media loading`)}</div>
  `}var io=class extends v.HTMLElement{constructor(){if(super(),Xa(this,Qa,void 0),Xa(this,$a,to),!this.shadowRoot){this.attachShadow(this.constructor.shadowRootOptions);let e=b(this.attributes);this.shadowRoot.innerHTML=this.constructor.getTemplateHTML(e)}}static get observedAttributes(){return[t.MEDIA_CONTROLLER,i.MEDIA_PAUSED,i.MEDIA_LOADING,eo.LOADING_DELAY]}attributeChangedCallback(e,n,r){var i,a,o,s;e===eo.LOADING_DELAY&&n!==r?this.loadingDelay=Number(r):e===t.MEDIA_CONTROLLER&&(n&&((a=(i=Ya(this,Qa))?.unassociateElement)==null||a.call(i,this),Za(this,Qa,null)),r&&this.isConnected&&(Za(this,Qa,this.getRootNode()?.getElementById(r)),(s=(o=Ya(this,Qa))?.associateElement)==null||s.call(o,this)))}connectedCallback(){var e,n;let r=this.getAttribute(t.MEDIA_CONTROLLER);r&&(Za(this,Qa,this.getRootNode()?.getElementById(r)),(n=(e=Ya(this,Qa))?.associateElement)==null||n.call(e,this))}disconnectedCallback(){var e,t;(t=(e=Ya(this,Qa))?.unassociateElement)==null||t.call(e,this),Za(this,Qa,null)}get loadingDelay(){return Ya(this,$a)}set loadingDelay(e){Za(this,$a,e);let{style:t}=x(this.shadowRoot,`:host`);t.setProperty(`--_loading-indicator-delay`,`var(--media-loading-indicator-transition-delay, ${e}ms)`)}get mediaPaused(){return w(this,i.MEDIA_PAUSED)}set mediaPaused(e){T(this,i.MEDIA_PAUSED,e)}get mediaLoading(){return w(this,i.MEDIA_LOADING)}set mediaLoading(e){T(this,i.MEDIA_LOADING,e)}get mediaController(){return E(this,t.MEDIA_CONTROLLER)}set mediaController(e){D(this,t.MEDIA_CONTROLLER,e)}get noAutohide(){return w(this,eo.NO_AUTOHIDE)}set noAutohide(e){T(this,eo.NO_AUTOHIDE,e)}};Qa=new WeakMap,$a=new WeakMap,io.shadowRootOptions={mode:`open`},io.getTemplateHTML=ro,v.customElements.get(`media-loading-indicator`)||v.customElements.define(`media-loading-indicator`,io);var ao=`<svg aria-hidden="true" viewBox="0 0 24 24">
  <path d="M16.5 12A4.5 4.5 0 0 0 14 8v2.18l2.45 2.45a4.22 4.22 0 0 0 .05-.63Zm2.5 0a6.84 6.84 0 0 1-.54 2.64L20 16.15A8.8 8.8 0 0 0 21 12a9 9 0 0 0-7-8.77v2.06A7 7 0 0 1 19 12ZM4.27 3 3 4.27 7.73 9H3v6h4l5 5v-6.73l4.25 4.25A6.92 6.92 0 0 1 14 18.7v2.06A9 9 0 0 0 17.69 19l2 2.05L21 19.73l-9-9L4.27 3ZM12 4 9.91 6.09 12 8.18V4Z"/>
</svg>`,oo=`<svg aria-hidden="true" viewBox="0 0 24 24">
  <path d="M3 9v6h4l5 5V4L7 9H3Zm13.5 3A4.5 4.5 0 0 0 14 8v8a4.47 4.47 0 0 0 2.5-4Z"/>
</svg>`,so=`<svg aria-hidden="true" viewBox="0 0 24 24">
  <path d="M3 9v6h4l5 5V4L7 9H3Zm13.5 3A4.5 4.5 0 0 0 14 8v8a4.47 4.47 0 0 0 2.5-4ZM14 3.23v2.06a7 7 0 0 1 0 13.42v2.06a9 9 0 0 0 0-17.54Z"/>
</svg>`;function co(e){return`
    <style>
      :host(:not([${i.MEDIA_VOLUME_LEVEL}])) slot[name=icon] slot:not([name=high]),
      :host([${i.MEDIA_VOLUME_LEVEL}=high]) slot[name=icon] slot:not([name=high]) {
        display: none !important;
      }

      :host([${i.MEDIA_VOLUME_LEVEL}=off]) slot[name=icon] slot:not([name=off]) {
        display: none !important;
      }

      :host([${i.MEDIA_VOLUME_LEVEL}=low]) slot[name=icon] slot:not([name=low]) {
        display: none !important;
      }

      :host([${i.MEDIA_VOLUME_LEVEL}=medium]) slot[name=icon] slot:not([name=medium]) {
        display: none !important;
      }

      :host(:not([${i.MEDIA_VOLUME_LEVEL}=off])) slot[name=tooltip-unmute],
      :host([${i.MEDIA_VOLUME_LEVEL}=off]) slot[name=tooltip-mute] {
        display: none;
      }
    </style>

    <slot name="icon">
      <slot name="off">${ao}</slot>
      <slot name="low">${oo}</slot>
      <slot name="medium">${oo}</slot>
      <slot name="high">${so}</slot>
    </slot>
  `}function lo(){return`
    <slot name="tooltip-mute">${_(`Mute`)}</slot>
    <slot name="tooltip-unmute">${_(`Unmute`)}</slot>
  `}var uo=e=>{let t=e.mediaVolumeLevel===`off`?_(`unmute`):_(`mute`);e.setAttribute(`aria-label`,t)},fo=class extends H{static get observedAttributes(){return[...super.observedAttributes,i.MEDIA_VOLUME_LEVEL]}connectedCallback(){super.connectedCallback(),uo(this)}attributeChangedCallback(e,t,n){super.attributeChangedCallback(e,t,n),e===i.MEDIA_VOLUME_LEVEL&&uo(this)}get mediaVolumeLevel(){return E(this,i.MEDIA_VOLUME_LEVEL)}set mediaVolumeLevel(e){D(this,i.MEDIA_VOLUME_LEVEL,e)}handleClick(){let t=this.mediaVolumeLevel===`off`?e.MEDIA_UNMUTE_REQUEST:e.MEDIA_MUTE_REQUEST;this.dispatchEvent(new v.CustomEvent(t,{composed:!0,bubbles:!0}))}};fo.getSlotTemplateHTML=co,fo.getTooltipContentHTML=lo,v.customElements.get(`media-mute-button`)||v.customElements.define(`media-mute-button`,fo);var po=`<svg aria-hidden="true" viewBox="0 0 28 24">
  <path d="M24 3H4a1 1 0 0 0-1 1v16a1 1 0 0 0 1 1h20a1 1 0 0 0 1-1V4a1 1 0 0 0-1-1Zm-1 16H5V5h18v14Zm-3-8h-7v5h7v-5Z"/>
</svg>`;function mo(e){return`
    <style>
      :host([${i.MEDIA_IS_PIP}]) slot[name=icon] slot:not([name=exit]) {
        display: none !important;
      }

      :host(:not([${i.MEDIA_IS_PIP}])) slot[name=icon] slot:not([name=enter]) {
        display: none !important;
      }

      :host([${i.MEDIA_IS_PIP}]) slot[name=tooltip-enter],
      :host(:not([${i.MEDIA_IS_PIP}])) slot[name=tooltip-exit] {
        display: none;
      }
    </style>

    <slot name="icon">
      <slot name="enter">${po}</slot>
      <slot name="exit">${po}</slot>
    </slot>
  `}function ho(){return`
    <slot name="tooltip-enter">${_(`Enter picture in picture mode`)}</slot>
    <slot name="tooltip-exit">${_(`Exit picture in picture mode`)}</slot>
  `}var go=e=>{let t=e.mediaIsPip?_(`exit picture in picture mode`):_(`enter picture in picture mode`);e.setAttribute(`aria-label`,t)},_o=class extends H{static get observedAttributes(){return[...super.observedAttributes,i.MEDIA_IS_PIP,i.MEDIA_PIP_UNAVAILABLE]}connectedCallback(){super.connectedCallback(),go(this)}attributeChangedCallback(e,t,n){super.attributeChangedCallback(e,t,n),e===i.MEDIA_IS_PIP&&go(this)}get mediaPipUnavailable(){return E(this,i.MEDIA_PIP_UNAVAILABLE)}set mediaPipUnavailable(e){D(this,i.MEDIA_PIP_UNAVAILABLE,e)}get mediaIsPip(){return w(this,i.MEDIA_IS_PIP)}set mediaIsPip(e){T(this,i.MEDIA_IS_PIP,e)}handleClick(){let t=this.mediaIsPip?e.MEDIA_EXIT_PIP_REQUEST:e.MEDIA_ENTER_PIP_REQUEST;this.dispatchEvent(new v.CustomEvent(t,{composed:!0,bubbles:!0}))}};_o.getSlotTemplateHTML=mo,_o.getTooltipContentHTML=ho,v.customElements.get(`media-pip-button`)||v.customElements.define(`media-pip-button`,_o);var vo=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},yo=(e,t,n)=>(vo(e,t,`read from private field`),n?n.call(e):t.get(e)),bo=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},xo,So={RATES:`rates`},Co=[1,1.2,1.5,1.7,2];function wo(e){return Math.round(e*100)/100}function To(e){return`
    <style>
      :host {
        min-width: 5ch;
        padding: var(--media-button-padding, var(--media-control-padding, 10px 5px));
      }
    </style>
    <slot name="icon">${e.mediaplaybackrate?wo(+e.mediaplaybackrate):1}x</slot>
  `}function Eo(){return _(`Playback rate`)}var Do=class extends H{constructor(){super(),bo(this,xo,new kt(this,So.RATES,{defaultValue:Co})),this.container=this.shadowRoot.querySelector(`slot[name="icon"]`),this.container.innerHTML=`${wo(this.mediaPlaybackRate??1)}x`}static get observedAttributes(){return[...super.observedAttributes,i.MEDIA_PLAYBACK_RATE,So.RATES]}attributeChangedCallback(e,t,n){if(super.attributeChangedCallback(e,t,n),e===So.RATES&&(yo(this,xo).value=n),e===i.MEDIA_PLAYBACK_RATE){let e=n?+n:NaN,t=wo(Number.isNaN(e)?1:e);this.container.innerHTML=`${t}x`,this.setAttribute(`aria-label`,_(`Playback rate {playbackRate}`,{playbackRate:t}))}}get rates(){return yo(this,xo)}set rates(e){e?Array.isArray(e)?yo(this,xo).value=e.join(` `):typeof e==`string`&&(yo(this,xo).value=e):yo(this,xo).value=``}get mediaPlaybackRate(){return S(this,i.MEDIA_PLAYBACK_RATE,1)}set mediaPlaybackRate(e){C(this,i.MEDIA_PLAYBACK_RATE,e)}handleClick(){let t=Array.from(yo(this,xo).values(),e=>+e).sort((e,t)=>e-t),n=t.find(e=>e>this.mediaPlaybackRate)??t[0]??1,r=new v.CustomEvent(e.MEDIA_PLAYBACK_RATE_REQUEST,{composed:!0,bubbles:!0,detail:n});this.dispatchEvent(r)}};xo=new WeakMap,Do.getSlotTemplateHTML=To,Do.getTooltipContentHTML=Eo,v.customElements.get(`media-playback-rate-button`)||v.customElements.define(`media-playback-rate-button`,Do);var Oo=`<svg aria-hidden="true" viewBox="0 0 24 24">
  <path d="m6 21 15-9L6 3v18Z"/>
</svg>`,ko=`<svg aria-hidden="true" viewBox="0 0 24 24">
  <path d="M6 20h4V4H6v16Zm8-16v16h4V4h-4Z"/>
</svg>`;function Ao(e){return`
    <style>
      :host([${i.MEDIA_PAUSED}]) slot[name=pause],
      :host(:not([${i.MEDIA_PAUSED}])) slot[name=play] {
        display: none !important;
      }

      :host([${i.MEDIA_PAUSED}]) slot[name=tooltip-pause],
      :host(:not([${i.MEDIA_PAUSED}])) slot[name=tooltip-play] {
        display: none;
      }
    </style>

    <slot name="icon">
      <slot name="play">${Oo}</slot>
      <slot name="pause">${ko}</slot>
    </slot>
  `}function jo(){return`
    <slot name="tooltip-play">${_(`Play`)}</slot>
    <slot name="tooltip-pause">${_(`Pause`)}</slot>
  `}var Mo=e=>{let t=e.mediaPaused?_(`play`):_(`pause`);e.setAttribute(`aria-label`,t)},No=class extends H{static get observedAttributes(){return[...super.observedAttributes,i.MEDIA_PAUSED,i.MEDIA_ENDED]}connectedCallback(){super.connectedCallback(),Mo(this)}attributeChangedCallback(e,t,n){super.attributeChangedCallback(e,t,n),(e===i.MEDIA_PAUSED||e===i.MEDIA_LANG)&&Mo(this)}get mediaPaused(){return w(this,i.MEDIA_PAUSED)}set mediaPaused(e){T(this,i.MEDIA_PAUSED,e)}handleClick(){let t=this.mediaPaused?e.MEDIA_PLAY_REQUEST:e.MEDIA_PAUSE_REQUEST;this.dispatchEvent(new v.CustomEvent(t,{composed:!0,bubbles:!0}))}};No.getSlotTemplateHTML=Ao,No.getTooltipContentHTML=jo,v.customElements.get(`media-play-button`)||v.customElements.define(`media-play-button`,No);var Po={PLACEHOLDER_SRC:`placeholdersrc`,SRC:`src`};function Fo(e){return`
    <style>
      :host {
        pointer-events: none;
        display: var(--media-poster-image-display, inline-block);
        box-sizing: border-box;
      }

      img {
        max-width: 100%;
        max-height: 100%;
        min-width: 100%;
        min-height: 100%;
        background-repeat: no-repeat;
        background-position: var(--media-poster-image-background-position, var(--media-object-position, center));
        background-size: var(--media-poster-image-background-size, var(--media-object-fit, contain));
        object-fit: var(--media-object-fit, contain);
        object-position: var(--media-object-position, center);
      }
    </style>

    <img part="poster img" aria-hidden="true" id="image"/>
  `}var Io=e=>{e.style.removeProperty(`background-image`)},Lo=(e,t)=>{e.style[`background-image`]=`url('${t}')`},Ro=class extends v.HTMLElement{static get observedAttributes(){return[Po.PLACEHOLDER_SRC,Po.SRC]}constructor(){if(super(),!this.shadowRoot){this.attachShadow(this.constructor.shadowRootOptions);let e=b(this.attributes);this.shadowRoot.innerHTML=this.constructor.getTemplateHTML(e)}this.image=this.shadowRoot.querySelector(`#image`)}attributeChangedCallback(e,t,n){e===Po.SRC&&(n==null?this.image.removeAttribute(Po.SRC):this.image.setAttribute(Po.SRC,n)),e===Po.PLACEHOLDER_SRC&&(n==null?Io(this.image):Lo(this.image,n))}get placeholderSrc(){return E(this,Po.PLACEHOLDER_SRC)}set placeholderSrc(e){D(this,Po.SRC,e)}get src(){return E(this,Po.SRC)}set src(e){D(this,Po.SRC,e)}};Ro.shadowRootOptions={mode:`open`},Ro.getTemplateHTML=Fo,v.customElements.get(`media-poster-image`)||v.customElements.define(`media-poster-image`,Ro);var zo=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},Bo=(e,t,n)=>(zo(e,t,`read from private field`),n?n.call(e):t.get(e)),Vo=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},Ho=(e,t,n,r)=>(zo(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),Uo,Wo=class extends ea{constructor(){super(),Vo(this,Uo,void 0),Ho(this,Uo,this.shadowRoot.querySelector(`slot`))}static get observedAttributes(){return[...super.observedAttributes,i.MEDIA_PREVIEW_CHAPTER,i.MEDIA_LANG]}attributeChangedCallback(e,t,n){if(super.attributeChangedCallback(e,t,n),(e===i.MEDIA_PREVIEW_CHAPTER||e===i.MEDIA_LANG)&&n!==t&&n!=null){if(Bo(this,Uo).textContent=n,n!==``){let e=_(`chapter: {chapterName}`,{chapterName:n});this.setAttribute(`aria-valuetext`,e)}else this.removeAttribute(`aria-valuetext`)}}get mediaPreviewChapter(){return E(this,i.MEDIA_PREVIEW_CHAPTER)}set mediaPreviewChapter(e){D(this,i.MEDIA_PREVIEW_CHAPTER,e)}};Uo=new WeakMap,v.customElements.get(`media-preview-chapter-display`)||v.customElements.define(`media-preview-chapter-display`,Wo);var Go=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},Ko=(e,t,n)=>(Go(e,t,`read from private field`),n?n.call(e):t.get(e)),qo=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},Jo=(e,t,n,r)=>(Go(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),Yo;function Xo(e){return`
    <style>
      :host {
        box-sizing: border-box;
        display: var(--media-control-display, var(--media-preview-thumbnail-display, inline-block));
        overflow: hidden;
      }

      img {
        display: none;
        position: relative;
      }
    </style>
    <img crossorigin loading="eager" decoding="async">
  `}var Zo=class extends v.HTMLElement{constructor(){if(super(),qo(this,Yo,void 0),!this.shadowRoot){this.attachShadow(this.constructor.shadowRootOptions);let e=b(this.attributes);this.shadowRoot.innerHTML=this.constructor.getTemplateHTML(e)}}static get observedAttributes(){return[t.MEDIA_CONTROLLER,i.MEDIA_PREVIEW_IMAGE,i.MEDIA_PREVIEW_COORDS]}connectedCallback(){var e,n;let r=this.getAttribute(t.MEDIA_CONTROLLER);r&&(Jo(this,Yo,this.getRootNode()?.getElementById(r)),(n=(e=Ko(this,Yo))?.associateElement)==null||n.call(e,this))}disconnectedCallback(){var e,t;(t=(e=Ko(this,Yo))?.unassociateElement)==null||t.call(e,this),Jo(this,Yo,null)}attributeChangedCallback(e,n,r){var a,o,s,c;[i.MEDIA_PREVIEW_IMAGE,i.MEDIA_PREVIEW_COORDS].includes(e)&&this.update(),e===t.MEDIA_CONTROLLER&&(n&&((o=(a=Ko(this,Yo))?.unassociateElement)==null||o.call(a,this),Jo(this,Yo,null)),r&&this.isConnected&&(Jo(this,Yo,this.getRootNode()?.getElementById(r)),(c=(s=Ko(this,Yo))?.associateElement)==null||c.call(s,this)))}get mediaPreviewImage(){return E(this,i.MEDIA_PREVIEW_IMAGE)}set mediaPreviewImage(e){D(this,i.MEDIA_PREVIEW_IMAGE,e)}get mediaPreviewCoords(){let e=this.getAttribute(i.MEDIA_PREVIEW_COORDS);if(e)return e.split(/\s+/).map(e=>+e)}set mediaPreviewCoords(e){if(!e){this.removeAttribute(i.MEDIA_PREVIEW_COORDS);return}this.setAttribute(i.MEDIA_PREVIEW_COORDS,e.join(` `))}update(){let e=this.mediaPreviewCoords,t=this.mediaPreviewImage;if(!(e&&t))return;let[n,r,i,a]=e,o=t.split(`#`)[0],s=getComputedStyle(this),{maxWidth:c,maxHeight:l,minWidth:u,minHeight:d}=s,f=s.getPropertyValue(`--media-preview-thumbnail-object-fit`).trim()||`contain`,p,m;if(f===`fill`){let e=parseInt(c)/i,t=parseInt(l)/a,n=parseInt(u)/i,r=parseInt(d)/a;p=e<1?e:Math.max(e,n),m=t<1?t:Math.max(t,r)}else{let e=Math.min(parseInt(c)/i,parseInt(l)/a),t=Math.max(parseInt(u)/i,parseInt(d)/a),n=e<1?e:t>1?t:1;p=n,m=n}let{style:h}=x(this.shadowRoot,`:host`),ee=x(this.shadowRoot,`img`).style,te=this.shadowRoot.querySelector(`img`),ne=Math.min(p,m)<1?`min`:`max`;h.setProperty(`${ne}-width`,`initial`,`important`),h.setProperty(`${ne}-height`,`initial`,`important`),h.width=`${i*p}px`,h.height=`${a*m}px`;let g=()=>{ee.width=`${this.imgWidth*p}px`,ee.height=`${this.imgHeight*m}px`,ee.display=`block`};te.src!==o&&(te.onload=()=>{this.imgWidth=te.naturalWidth,this.imgHeight=te.naturalHeight,g(),te.onload=null},te.src=o,g()),g(),ee.transform=`translate(-${n*p}px, -${r*m}px)`}};Yo=new WeakMap,Zo.shadowRootOptions={mode:`open`},Zo.getTemplateHTML=Xo,v.customElements.get(`media-preview-thumbnail`)||v.customElements.define(`media-preview-thumbnail`,Zo);var Qo=Zo,$o=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},es=(e,t,n)=>($o(e,t,`read from private field`),n?n.call(e):t.get(e)),ts=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},ns=(e,t,n,r)=>($o(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),rs,is=class extends ea{constructor(){super(),ts(this,rs,void 0),ns(this,rs,this.shadowRoot.querySelector(`slot`)),es(this,rs).textContent=de(0)}static get observedAttributes(){return[...super.observedAttributes,i.MEDIA_PREVIEW_TIME]}attributeChangedCallback(e,t,n){super.attributeChangedCallback(e,t,n),e===i.MEDIA_PREVIEW_TIME&&n!=null&&(es(this,rs).textContent=de(parseFloat(n)))}get mediaPreviewTime(){return S(this,i.MEDIA_PREVIEW_TIME)}set mediaPreviewTime(e){C(this,i.MEDIA_PREVIEW_TIME,e)}};rs=new WeakMap,v.customElements.get(`media-preview-time-display`)||v.customElements.define(`media-preview-time-display`,is);var as={SEEK_OFFSET:`seekoffset`},os=30,ss=e=>`
  <svg aria-hidden="true" viewBox="0 0 20 24">
    <defs>
      <style>.text{font-size:8px;font-family:Arial-BoldMT, Arial;font-weight:700;}</style>
    </defs>
    <text class="text value" transform="translate(2.18 19.87)">${e}</text>
    <path d="M10 6V3L4.37 7 10 10.94V8a5.54 5.54 0 0 1 1.9 10.48v2.12A7.5 7.5 0 0 0 10 6Z"/>
  </svg>`;function cs(e,t){return`
    <slot name="icon">${ss(t.seekOffset)}</slot>
  `}var ls=(e,t)=>{e.setAttribute(`aria-label`,_(`seek back {seekOffset} seconds`,{seekOffset:t}))};function us(){return _(`Seek backward`)}var ds=0,fs=class extends H{static get observedAttributes(){return[...super.observedAttributes,i.MEDIA_CURRENT_TIME,as.SEEK_OFFSET]}connectedCallback(){super.connectedCallback(),this.seekOffset=S(this,as.SEEK_OFFSET,os)}attributeChangedCallback(e,t,n){super.attributeChangedCallback(e,t,n),ls(this,this.seekOffset),e===as.SEEK_OFFSET&&(this.seekOffset=S(this,as.SEEK_OFFSET,os))}get seekOffset(){return S(this,as.SEEK_OFFSET,os)}set seekOffset(e){C(this,as.SEEK_OFFSET,e),this.setAttribute(`aria-label`,_(`seek back {seekOffset} seconds`,{seekOffset:this.seekOffset})),De(ke(this,`icon`),this.seekOffset)}get mediaCurrentTime(){return S(this,i.MEDIA_CURRENT_TIME,ds)}set mediaCurrentTime(e){C(this,i.MEDIA_CURRENT_TIME,e)}handleClick(){let t=Math.max(this.mediaCurrentTime-this.seekOffset,0),n=new v.CustomEvent(e.MEDIA_SEEK_REQUEST,{composed:!0,bubbles:!0,detail:t});this.dispatchEvent(n)}};fs.getSlotTemplateHTML=cs,fs.getTooltipContentHTML=us,v.customElements.get(`media-seek-backward-button`)||v.customElements.define(`media-seek-backward-button`,fs);var ps={SEEK_OFFSET:`seekoffset`},ms=30,hs=e=>`
  <svg aria-hidden="true" viewBox="0 0 20 24">
    <defs>
      <style>.text{font-size:8px;font-family:Arial-BoldMT, Arial;font-weight:700;}</style>
    </defs>
    <text class="text value" transform="translate(8.9 19.87)">${e}</text>
    <path d="M10 6V3l5.61 4L10 10.94V8a5.54 5.54 0 0 0-1.9 10.48v2.12A7.5 7.5 0 0 1 10 6Z"/>
  </svg>`;function gs(e,t){return`
    <slot name="icon">${hs(t.seekOffset)}</slot>
  `}var _s=(e,t)=>{e.setAttribute(`aria-label`,_(`seek forward {seekOffset} seconds`,{seekOffset:t}))};function vs(){return _(`Seek forward`)}var ys=0,bs=class extends H{static get observedAttributes(){return[...super.observedAttributes,i.MEDIA_CURRENT_TIME,ps.SEEK_OFFSET]}connectedCallback(){super.connectedCallback(),this.seekOffset=S(this,ps.SEEK_OFFSET,ms)}attributeChangedCallback(e,t,n){super.attributeChangedCallback(e,t,n),_s(this,this.seekOffset),e===ps.SEEK_OFFSET&&(this.seekOffset=S(this,ps.SEEK_OFFSET,ms))}get seekOffset(){return S(this,ps.SEEK_OFFSET,ms)}set seekOffset(e){C(this,ps.SEEK_OFFSET,e),this.setAttribute(`aria-label`,_(`seek forward {seekOffset} seconds`,{seekOffset:this.seekOffset})),De(ke(this,`icon`),this.seekOffset)}get mediaCurrentTime(){return S(this,i.MEDIA_CURRENT_TIME,ys)}set mediaCurrentTime(e){C(this,i.MEDIA_CURRENT_TIME,e)}handleClick(){let t=this.mediaCurrentTime+this.seekOffset,n=new v.CustomEvent(e.MEDIA_SEEK_REQUEST,{composed:!0,bubbles:!0,detail:t});this.dispatchEvent(n)}};bs.getSlotTemplateHTML=gs,bs.getTooltipContentHTML=vs,v.customElements.get(`media-seek-forward-button`)||v.customElements.define(`media-seek-forward-button`,bs);var xs=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},q=(e,t,n)=>(xs(e,t,`read from private field`),n?n.call(e):t.get(e)),Ss=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},Cs=(e,t,n,r)=>(xs(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),ws=(e,t,n)=>(xs(e,t,`access private method`),n),Ts,Es,Ds,Os,ks,As,js,Ms,Ns,Ps,Fs,Is={REMAINING:`remaining`,SHOW_DURATION:`showduration`,NO_TOGGLE:`notoggle`},Ls=[...Object.values(Is),i.MEDIA_CURRENT_TIME,i.MEDIA_DURATION,i.MEDIA_SEEKABLE],Rs=[`Enter`,` `],zs=`&nbsp;/&nbsp;`,Bs=(e,{timesSep:t=zs}={})=>{let n=e.mediaCurrentTime??0,[,r]=e.mediaSeekable??[],i=0;Number.isFinite(e.mediaDuration)?i=e.mediaDuration:Number.isFinite(r)&&(i=r);let a=e.remaining?de(0-(i-n)):de(n);return e.showDuration?`${a}${t}${de(i)}`:a},Vs=e=>{let t=e.mediaCurrentTime,[,n]=e.mediaSeekable??[],r=null;if(Number.isFinite(e.mediaDuration)?r=e.mediaDuration:Number.isFinite(n)&&(r=n),t==null||r===null){e.setAttribute(`aria-description`,_(`video not loaded, unknown time.`));return}let i=e.remaining?ue(0-(r-t)):ue(t);if(!e.showDuration){e.setAttribute(`aria-description`,i);return}let a=_(`{currentTime} of {totalTime}`,{currentTime:i,totalTime:ue(r)});e.setAttribute(`aria-description`,a)};function Hs(e,t){return`
    <slot>${Bs(t)}</slot>
  `}var Us=e=>{e.setAttribute(`aria-label`,_(`playback time`))},Ws=class extends ea{constructor(){super(),Ss(this,Os),Ss(this,As),Ss(this,Ms),Ss(this,Ps),Ss(this,Ts,void 0),Ss(this,Es,null),Ss(this,Ds,e=>{let{metaKey:t,altKey:n,key:r}=e;if(t||n||!Rs.includes(r)){this.removeEventListener(`keyup`,q(this,Es));return}this.addEventListener(`keyup`,q(this,Es))}),Cs(this,Ts,this.shadowRoot.querySelector(`slot`)),q(this,Ts).innerHTML=`${Bs(this)}`}static get observedAttributes(){return[...super.observedAttributes,...Ls,`disabled`]}connectedCallback(){let{style:e}=x(this.shadowRoot,`:host(:hover:not([notoggle]))`);e.setProperty(`cursor`,`var(--media-cursor, pointer)`),e.setProperty(`background`,`var(--media-control-hover-background, rgba(50 50 70 / .7))`),this.setAttribute(`aria-label`,_(`playback time`)),ws(this,Ms,Ns).call(this),super.connectedCallback()}toggleTimeDisplay(){this.noToggle||(this.hasAttribute(`remaining`)?this.removeAttribute(`remaining`):this.setAttribute(`remaining`,``))}disconnectedCallback(){this.disable(),ws(this,As,js).call(this),super.disconnectedCallback()}attributeChangedCallback(e,t,n){Us(this),Ls.includes(e)?this.update():e===`disabled`&&n!==t?n==null?ws(this,Ms,Ns).call(this):ws(this,Ps,Fs).call(this):e===Is.NO_TOGGLE&&n!==t&&(this.noToggle?ws(this,Ps,Fs).call(this):ws(this,Ms,Ns).call(this)),super.attributeChangedCallback(e,t,n)}enable(){this.noToggle||(this.tabIndex=0)}disable(){this.tabIndex=-1}get remaining(){return w(this,Is.REMAINING)}set remaining(e){T(this,Is.REMAINING,e)}get showDuration(){return w(this,Is.SHOW_DURATION)}set showDuration(e){T(this,Is.SHOW_DURATION,e)}get noToggle(){return w(this,Is.NO_TOGGLE)}set noToggle(e){T(this,Is.NO_TOGGLE,e)}get mediaDuration(){return S(this,i.MEDIA_DURATION)}set mediaDuration(e){C(this,i.MEDIA_DURATION,e)}get mediaCurrentTime(){return S(this,i.MEDIA_CURRENT_TIME)}set mediaCurrentTime(e){C(this,i.MEDIA_CURRENT_TIME,e)}get mediaSeekable(){let e=this.getAttribute(i.MEDIA_SEEKABLE);if(e)return e.split(`:`).map(e=>+e)}set mediaSeekable(e){if(e==null){this.removeAttribute(i.MEDIA_SEEKABLE);return}this.setAttribute(i.MEDIA_SEEKABLE,e.join(`:`))}update(){let e=Bs(this);Vs(this),e!==q(this,Ts).innerHTML&&(q(this,Ts).innerHTML=e)}};Ts=new WeakMap,Es=new WeakMap,Ds=new WeakMap,Os=new WeakSet,ks=function(){q(this,Es)||(Cs(this,Es,e=>{let{key:t}=e;if(!Rs.includes(t)){this.removeEventListener(`keyup`,q(this,Es));return}this.toggleTimeDisplay()}),this.addEventListener(`keydown`,q(this,Ds)),this.addEventListener(`click`,this.toggleTimeDisplay))},As=new WeakSet,js=function(){q(this,Es)&&(this.removeEventListener(`keyup`,q(this,Es)),this.removeEventListener(`keydown`,q(this,Ds)),this.removeEventListener(`click`,this.toggleTimeDisplay),Cs(this,Es,null))},Ms=new WeakSet,Ns=function(){!this.noToggle&&!this.hasAttribute(`disabled`)&&(this.setAttribute(`role`,`button`),this.enable(),ws(this,Os,ks).call(this))},Ps=new WeakSet,Fs=function(){this.removeAttribute(`role`),this.disable(),ws(this,As,js).call(this)},Ws.getSlotTemplateHTML=Hs,v.customElements.get(`media-time-display`)||v.customElements.define(`media-time-display`,Ws);var Gs=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},J=(e,t,n)=>(Gs(e,t,`read from private field`),n?n.call(e):t.get(e)),Ks=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},Y=(e,t,n,r)=>(Gs(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),qs=(e,t,n,r)=>({set _(r){Y(e,t,r,n)},get _(){return J(e,t,r)}}),Js,Ys,Xs,Zs,Qs,$s,ec,tc,nc,rc,ic=class{constructor(e,t,n){Ks(this,Js,void 0),Ks(this,Ys,void 0),Ks(this,Xs,void 0),Ks(this,Zs,void 0),Ks(this,Qs,void 0),Ks(this,$s,void 0),Ks(this,ec,void 0),Ks(this,tc,void 0),Ks(this,nc,0),Ks(this,rc,(e=performance.now())=>{Y(this,nc,requestAnimationFrame(J(this,rc))),Y(this,Zs,performance.now()-J(this,Xs));let t=1e3/this.fps;if(J(this,Zs)>t){Y(this,Xs,e-J(this,Zs)%t);let n=1e3/((e-J(this,Ys))/++qs(this,Qs)._),r=(e-J(this,$s))/1e3/this.duration,i=J(this,ec)+r*this.playbackRate;i-J(this,Js).valueAsNumber>0?Y(this,tc,this.playbackRate/this.duration/n):(Y(this,tc,.995*J(this,tc)),i=J(this,Js).valueAsNumber+J(this,tc)),this.callback(i)}}),Y(this,Js,e),this.callback=t,this.fps=n}start(){J(this,nc)===0&&(Y(this,Xs,performance.now()),Y(this,Ys,J(this,Xs)),Y(this,Qs,0),J(this,rc).call(this))}stop(){J(this,nc)!==0&&(cancelAnimationFrame(J(this,nc)),Y(this,nc,0))}update({start:e,duration:t,playbackRate:n}){let r=e-J(this,Js).valueAsNumber,i=Math.abs(t-this.duration);(r>0||r<-.03||i>=.5)&&this.callback(e),Y(this,ec,e),Y(this,$s,performance.now()),this.duration=t,this.playbackRate=n}};Js=new WeakMap,Ys=new WeakMap,Xs=new WeakMap,Zs=new WeakMap,Qs=new WeakMap,$s=new WeakMap,ec=new WeakMap,tc=new WeakMap,nc=new WeakMap,rc=new WeakMap;var ac=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},X=(e,t,n)=>(ac(e,t,`read from private field`),n?n.call(e):t.get(e)),Z=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},Q=(e,t,n,r)=>(ac(e,t,`write to private field`),r?r.call(e,n):t.set(e,n),n),$=(e,t,n)=>(ac(e,t,`access private method`),n),oc,sc,cc,lc,uc,dc,fc,pc,mc,hc,gc,_c,vc,yc,bc,xc,Sc,Cc,wc,Tc,Ec,Dc,Oc,kc,Ac,jc,Mc=e=>{let t=e.range,n=ue(+Fc(e)),r=ue(+e.mediaSeekableEnd),i=n&&r?_(`{currentTime} of {totalTime}`,{currentTime:n,totalTime:r}):_(`video not loaded, unknown time.`);t.setAttribute(`aria-valuetext`,i)};function Nc(e){return`
    <style>
      :host {
        --media-box-border-radius: 4px;
        --media-box-padding-left: 10px;
        --media-box-padding-right: 10px;
        --media-preview-border-radius: var(--media-box-border-radius);
        --media-box-arrow-offset: var(--media-box-border-radius);
        --_control-background: var(--media-control-background, var(--media-secondary-color, rgb(20 20 30 / .7)));
        --_preview-background: var(--media-preview-background, var(--_control-background));

        
        contain: layout;
      }

      #buffered {
        background: var(--media-time-range-buffered-color, rgb(255 255 255 / .4));
        position: absolute;
        height: 100%;
        will-change: width;
      }

      #preview-rail,
      #current-rail {
        width: 100%;
        position: absolute;
        left: 0;
        bottom: 100%;
        pointer-events: none;
        will-change: transform;
      }

      [part~="box"] {
        width: min-content;
        
        position: absolute;
        bottom: 100%;
        flex-direction: column;
        align-items: center;
        transform: translateX(-50%);
      }

      [part~="current-box"] {
        display: var(--media-current-box-display, var(--media-box-display, flex));
        margin: var(--media-current-box-margin, var(--media-box-margin, 0 0 5px));
        visibility: hidden;
      }

      [part~="preview-box"] {
        display: var(--media-preview-box-display, var(--media-box-display, flex));
        margin: var(--media-preview-box-margin, var(--media-box-margin, 0 0 5px));
        transition-property: var(--media-preview-transition-property, visibility, opacity);
        transition-duration: var(--media-preview-transition-duration-out, .25s);
        transition-delay: var(--media-preview-transition-delay-out, 0s);
        visibility: hidden;
        opacity: 0;
      }

      :host(:is([${i.MEDIA_PREVIEW_IMAGE}], [${i.MEDIA_PREVIEW_TIME}])[dragging]) [part~="preview-box"] {
        transition-duration: var(--media-preview-transition-duration-in, .5s);
        transition-delay: var(--media-preview-transition-delay-in, .25s);
        visibility: visible;
        opacity: 1;
      }

      @media (hover: hover) {
        :host(:is([${i.MEDIA_PREVIEW_IMAGE}], [${i.MEDIA_PREVIEW_TIME}]):hover) [part~="preview-box"] {
          transition-duration: var(--media-preview-transition-duration-in, .5s);
          transition-delay: var(--media-preview-transition-delay-in, .25s);
          visibility: visible;
          opacity: 1;
        }
      }

      media-preview-thumbnail,
      ::slotted(media-preview-thumbnail) {
        visibility: hidden;
        
        transition: visibility 0s .25s;
        transition-delay: calc(var(--media-preview-transition-delay-out, 0s) + var(--media-preview-transition-duration-out, .25s));
        background: var(--media-preview-thumbnail-background, var(--_preview-background));
        box-shadow: var(--media-preview-thumbnail-box-shadow, 0 0 4px rgb(0 0 0 / .2));
        max-width: var(--media-preview-thumbnail-max-width, 180px);
        max-height: var(--media-preview-thumbnail-max-height, 160px);
        min-width: var(--media-preview-thumbnail-min-width, 120px);
        min-height: var(--media-preview-thumbnail-min-height, 80px);
        border: var(--media-preview-thumbnail-border);
        border-radius: var(--media-preview-thumbnail-border-radius,
          var(--media-preview-border-radius) var(--media-preview-border-radius) 0 0);
      }

      :host([${i.MEDIA_PREVIEW_IMAGE}][dragging]) media-preview-thumbnail,
      :host([${i.MEDIA_PREVIEW_IMAGE}][dragging]) ::slotted(media-preview-thumbnail) {
        transition-delay: var(--media-preview-transition-delay-in, .25s);
        visibility: visible;
      }

      @media (hover: hover) {
        :host([${i.MEDIA_PREVIEW_IMAGE}]:hover) media-preview-thumbnail,
        :host([${i.MEDIA_PREVIEW_IMAGE}]:hover) ::slotted(media-preview-thumbnail) {
          transition-delay: var(--media-preview-transition-delay-in, .25s);
          visibility: visible;
        }

        :host([${i.MEDIA_PREVIEW_TIME}]:hover) {
          --media-time-range-hover-display: block;
        }
      }

      media-preview-chapter-display,
      ::slotted(media-preview-chapter-display) {
        font-size: var(--media-font-size, 13px);
        line-height: 17px;
        min-width: 0;
        visibility: hidden;
        
        transition: min-width 0s, border-radius 0s, margin 0s, padding 0s, visibility 0s;
        transition-delay: calc(var(--media-preview-transition-delay-out, 0s) + var(--media-preview-transition-duration-out, .25s));
        background: var(--media-preview-chapter-background, var(--_preview-background));
        border-radius: var(--media-preview-chapter-border-radius,
          var(--media-preview-border-radius) var(--media-preview-border-radius)
          var(--media-preview-border-radius) var(--media-preview-border-radius));
        padding: var(--media-preview-chapter-padding, 3.5px 9px);
        margin: var(--media-preview-chapter-margin, 0 0 5px);
        text-shadow: var(--media-preview-chapter-text-shadow, 0 0 4px rgb(0 0 0 / .75));
      }

      :host([${i.MEDIA_PREVIEW_IMAGE}]) media-preview-chapter-display,
      :host([${i.MEDIA_PREVIEW_IMAGE}]) ::slotted(media-preview-chapter-display) {
        transition-delay: var(--media-preview-transition-delay-in, .25s);
        border-radius: var(--media-preview-chapter-border-radius, 0);
        padding: var(--media-preview-chapter-padding, 3.5px 9px 0);
        margin: var(--media-preview-chapter-margin, 0);
        min-width: 100%;
      }

      media-preview-chapter-display[${i.MEDIA_PREVIEW_CHAPTER}],
      ::slotted(media-preview-chapter-display[${i.MEDIA_PREVIEW_CHAPTER}]) {
        visibility: visible;
      }

      media-preview-chapter-display:not([aria-valuetext]),
      ::slotted(media-preview-chapter-display:not([aria-valuetext])) {
        display: none;
      }

      media-preview-time-display,
      ::slotted(media-preview-time-display),
      media-time-display,
      ::slotted(media-time-display) {
        font-size: var(--media-font-size, 13px);
        line-height: 17px;
        min-width: 0;
        
        transition: min-width 0s, border-radius 0s;
        transition-delay: calc(var(--media-preview-transition-delay-out, 0s) + var(--media-preview-transition-duration-out, .25s));
        background: var(--media-preview-time-background, var(--_preview-background));
        border-radius: var(--media-preview-time-border-radius,
          var(--media-preview-border-radius) var(--media-preview-border-radius)
          var(--media-preview-border-radius) var(--media-preview-border-radius));
        padding: var(--media-preview-time-padding, 3.5px 9px);
        margin: var(--media-preview-time-margin, 0);
        text-shadow: var(--media-preview-time-text-shadow, 0 0 4px rgb(0 0 0 / .75));
        transform: translateX(min(
          max(calc(50% - var(--_box-width) / 2),
          calc(var(--_box-shift, 0))),
          calc(var(--_box-width) / 2 - 50%)
        ));
      }

      :host([${i.MEDIA_PREVIEW_IMAGE}]) media-preview-time-display,
      :host([${i.MEDIA_PREVIEW_IMAGE}]) ::slotted(media-preview-time-display) {
        transition-delay: var(--media-preview-transition-delay-in, .25s);
        border-radius: var(--media-preview-time-border-radius,
          0 0 var(--media-preview-border-radius) var(--media-preview-border-radius));
        min-width: 100%;
      }

      :host([${i.MEDIA_PREVIEW_TIME}]:hover) {
        --media-time-range-hover-display: block;
      }

      [part~="arrow"],
      ::slotted([part~="arrow"]) {
        display: var(--media-box-arrow-display, inline-block);
        transform: translateX(min(
          max(calc(50% - var(--_box-width) / 2 + var(--media-box-arrow-offset)),
          calc(var(--_box-shift, 0))),
          calc(var(--_box-width) / 2 - 50% - var(--media-box-arrow-offset))
        ));
        
        border-color: transparent;
        border-top-color: var(--media-box-arrow-background, var(--_control-background));
        border-width: var(--media-box-arrow-border-width,
          var(--media-box-arrow-height, 5px) var(--media-box-arrow-width, 6px) 0);
        border-style: solid;
        justify-content: center;
        height: 0;
      }
    </style>
    <div id="preview-rail">
      <slot name="preview" part="box preview-box">
        <media-preview-thumbnail>
          <template shadowrootmode="${Qo.shadowRootOptions.mode}">
            ${Qo.getTemplateHTML({})}
          </template>
        </media-preview-thumbnail>
        <media-preview-chapter-display></media-preview-chapter-display>
        <media-preview-time-display></media-preview-time-display>
        <slot name="preview-arrow"><div part="arrow"></div></slot>
      </slot>
    </div>
    <div id="current-rail">
      <slot name="current" part="box current-box">
        
      </slot>
    </div>
  `}var Pc=(e,t=e.mediaCurrentTime)=>{let n=Number.isFinite(e.mediaSeekableStart)?e.mediaSeekableStart:0,r=Number.isFinite(e.mediaDuration)?e.mediaDuration:e.mediaSeekableEnd;if(Number.isNaN(r))return 0;let i=(t-n)/(r-n);return Math.max(0,Math.min(i,1))},Fc=(e,t=e.range.valueAsNumber)=>{let n=Number.isFinite(e.mediaSeekableStart)?e.mediaSeekableStart:0,r=Number.isFinite(e.mediaDuration)?e.mediaDuration:e.mediaSeekableEnd;return Number.isNaN(r)?0:t*(r-n)+n},Ic=class extends zi{constructor(){super(),Z(this,_c),Z(this,bc),Z(this,Sc),Z(this,wc),Z(this,Ec),Z(this,Oc),Z(this,Ac),Z(this,oc,null),Z(this,sc,void 0),Z(this,cc,void 0),Z(this,lc,void 0),Z(this,uc,void 0),Z(this,dc,void 0),Z(this,fc,void 0),Z(this,pc,void 0),Z(this,mc,void 0),Z(this,hc,void 0),Z(this,gc,()=>{$(this,_c,vc).call(this)?X(this,sc).start():X(this,sc).stop()}),Z(this,yc,e=>{this.dragging||(te(e)&&(this.range.valueAsNumber=e),X(this,hc)||this.updateBar())}),this.shadowRoot.querySelector(`#track`).insertAdjacentHTML(`afterbegin`,`<div id="buffered" part="buffered"></div>`),Q(this,cc,this.shadowRoot.querySelectorAll(`[part~="box"]`)),Q(this,uc,this.shadowRoot.querySelector(`[part~="preview-box"]`)),Q(this,dc,this.shadowRoot.querySelector(`[part~="current-box"]`));let e=getComputedStyle(this);Q(this,fc,parseInt(e.getPropertyValue(`--media-box-padding-left`))),Q(this,pc,parseInt(e.getPropertyValue(`--media-box-padding-right`))),Q(this,sc,new ic(this.range,X(this,yc),60))}static get observedAttributes(){return[...super.observedAttributes,i.MEDIA_PAUSED,i.MEDIA_DURATION,i.MEDIA_SEEKABLE,i.MEDIA_CURRENT_TIME,i.MEDIA_PREVIEW_IMAGE,i.MEDIA_PREVIEW_TIME,i.MEDIA_PREVIEW_CHAPTER,i.MEDIA_BUFFERED,i.MEDIA_PLAYBACK_RATE,i.MEDIA_LOADING,i.MEDIA_ENDED]}connectedCallback(){var e;super.connectedCallback(),this.range.setAttribute(`aria-label`,_(`seek`)),X(this,gc).call(this),Q(this,oc,this.getRootNode()),(e=X(this,oc))==null||e.addEventListener(`transitionstart`,this)}disconnectedCallback(){var e;super.disconnectedCallback(),X(this,sc).stop(),(e=X(this,oc))==null||e.removeEventListener(`transitionstart`,this),Q(this,oc,null)}attributeChangedCallback(e,t,n){super.attributeChangedCallback(e,t,n),t!=n&&(e===i.MEDIA_CURRENT_TIME||e===i.MEDIA_PAUSED||e===i.MEDIA_ENDED||e===i.MEDIA_LOADING||e===i.MEDIA_DURATION||e===i.MEDIA_SEEKABLE?(X(this,sc).update({start:Pc(this),duration:this.mediaSeekableEnd-this.mediaSeekableStart,playbackRate:this.mediaPlaybackRate}),X(this,gc).call(this),Mc(this)):e===i.MEDIA_BUFFERED&&this.updateBufferedBar(),(e===i.MEDIA_DURATION||e===i.MEDIA_SEEKABLE)&&(this.mediaChaptersCues=X(this,mc),this.updateBar()))}get mediaChaptersCues(){return X(this,mc)}set mediaChaptersCues(e){Q(this,mc,e),this.updateSegments(X(this,mc)?.map(e=>({start:Pc(this,e.startTime),end:Pc(this,e.endTime)})))}get mediaPaused(){return w(this,i.MEDIA_PAUSED)}set mediaPaused(e){T(this,i.MEDIA_PAUSED,e)}get mediaLoading(){return w(this,i.MEDIA_LOADING)}set mediaLoading(e){T(this,i.MEDIA_LOADING,e)}get mediaDuration(){return S(this,i.MEDIA_DURATION)}set mediaDuration(e){C(this,i.MEDIA_DURATION,e)}get mediaCurrentTime(){return S(this,i.MEDIA_CURRENT_TIME)}set mediaCurrentTime(e){C(this,i.MEDIA_CURRENT_TIME,e)}get mediaPlaybackRate(){return S(this,i.MEDIA_PLAYBACK_RATE,1)}set mediaPlaybackRate(e){C(this,i.MEDIA_PLAYBACK_RATE,e)}get mediaBuffered(){let e=this.getAttribute(i.MEDIA_BUFFERED);return e?e.split(` `).map(e=>e.split(`:`).map(e=>+e)):[]}set mediaBuffered(e){if(!e){this.removeAttribute(i.MEDIA_BUFFERED);return}let t=e.map(e=>e.join(`:`)).join(` `);this.setAttribute(i.MEDIA_BUFFERED,t)}get mediaSeekable(){let e=this.getAttribute(i.MEDIA_SEEKABLE);if(e)return e.split(`:`).map(e=>+e)}set mediaSeekable(e){if(e==null){this.removeAttribute(i.MEDIA_SEEKABLE);return}this.setAttribute(i.MEDIA_SEEKABLE,e.join(`:`))}get mediaSeekableEnd(){let[,e=this.mediaDuration]=this.mediaSeekable??[];return e}get mediaSeekableStart(){let[e=0]=this.mediaSeekable??[];return e}get mediaPreviewImage(){return E(this,i.MEDIA_PREVIEW_IMAGE)}set mediaPreviewImage(e){D(this,i.MEDIA_PREVIEW_IMAGE,e)}get mediaPreviewTime(){return S(this,i.MEDIA_PREVIEW_TIME)}set mediaPreviewTime(e){C(this,i.MEDIA_PREVIEW_TIME,e)}get mediaEnded(){return w(this,i.MEDIA_ENDED)}set mediaEnded(e){T(this,i.MEDIA_ENDED,e)}updateBar(){super.updateBar(),this.updateBufferedBar(),this.updateCurrentBox()}updateBufferedBar(){let e=this.mediaBuffered;if(!e.length)return;let t;if(this.mediaEnded)t=1;else{let n=this.mediaCurrentTime,[,r=this.mediaSeekableStart]=e.find(([e,t])=>e<=n&&n<=t)??[];t=Pc(this,r)}let{style:n}=x(this.shadowRoot,`#buffered`);n.setProperty(`width`,`${t*100}%`)}updateCurrentBox(){if(!this.shadowRoot.querySelector(`slot[name="current"]`).assignedElements().length)return;let e=x(this.shadowRoot,`#current-rail`),t=x(this.shadowRoot,`[part~="current-box"]`),n=$(this,bc,xc).call(this,X(this,dc)),r=$(this,Sc,Cc).call(this,n,this.range.valueAsNumber),i=$(this,wc,Tc).call(this,n,this.range.valueAsNumber);e.style.transform=`translateX(${r})`,e.style.setProperty(`--_range-width`,`${n.range.width}`),t.style.setProperty(`--_box-shift`,`${i}`),t.style.setProperty(`--_box-width`,`${n.box.width}px`),t.style.setProperty(`visibility`,`initial`)}handleEvent(e){switch(super.handleEvent(e),e.type){case`input`:$(this,Ac,jc).call(this);break;case`pointermove`:$(this,Ec,Dc).call(this,e);break;case`pointerup`:X(this,hc)&&Q(this,hc,!1);break;case`pointerdown`:Q(this,hc,!0);break;case`pointerleave`:$(this,Oc,kc).call(this,null);break;case`transitionstart`:Ae(e.target,this)&&setTimeout(()=>X(this,gc).call(this),0)}}};oc=new WeakMap,sc=new WeakMap,cc=new WeakMap,lc=new WeakMap,uc=new WeakMap,dc=new WeakMap,fc=new WeakMap,pc=new WeakMap,mc=new WeakMap,hc=new WeakMap,gc=new WeakMap,_c=new WeakSet,vc=function(){return this.isConnected&&!this.mediaPaused&&!this.mediaLoading&&!this.mediaEnded&&this.mediaSeekableEnd>0&&Pe(this)},yc=new WeakMap,bc=new WeakSet,xc=function(e){let t=((this.getAttribute(`bounds`)?je(this,`#${this.getAttribute(`bounds`)}`):this.parentElement)??this).getBoundingClientRect(),n=this.range.getBoundingClientRect(),r=e.offsetWidth;return{box:{width:r,min:-(n.left-t.left-r/2),max:t.right-n.left-r/2},bounds:t,range:n}},Sc=new WeakSet,Cc=function(e,t){let n=`${t*100}%`,{width:r,min:i,max:a}=e.box;if(!r)return n;if(Number.isNaN(i)||(n=`max(${`calc(1 / var(--_range-width) * 100 * ${i}% + var(--media-box-padding-left))`}, ${n})`),!Number.isNaN(a)){let e=`calc(1 / var(--_range-width) * 100 * ${a}% - var(--media-box-padding-right))`;n=`min(${n}, ${e})`}return n},wc=new WeakSet,Tc=function(e,t){let{width:n,min:r,max:i}=e.box,a=t*e.range.width;if(a<r+X(this,fc)){let t=e.range.left-e.bounds.left-X(this,fc);return`${a-n/2+t}px`}if(a>i-X(this,pc)){let t=e.bounds.right-e.range.right-X(this,pc);return`${a+n/2-t-e.range.width}px`}return 0},Ec=new WeakSet,Dc=function(e){let t=[...X(this,cc)].some(t=>e.composedPath().includes(t));if(!this.dragging&&(t||!e.composedPath().includes(this))){$(this,Oc,kc).call(this,null);return}let n=this.mediaSeekableEnd;if(!n)return;let r=x(this.shadowRoot,`#preview-rail`),i=x(this.shadowRoot,`[part~="preview-box"]`),a=$(this,bc,xc).call(this,X(this,uc)),o=(e.clientX-a.range.left)/a.range.width;o=Math.max(0,Math.min(1,o));let s=$(this,Sc,Cc).call(this,a,o),c=$(this,wc,Tc).call(this,a,o);r.style.transform=`translateX(${s})`,r.style.setProperty(`--_range-width`,`${a.range.width}`),i.style.setProperty(`--_box-shift`,`${c}`),i.style.setProperty(`--_box-width`,`${a.box.width}px`);let l=Math.round(X(this,lc))-Math.round(o*n);Math.abs(l)<1&&o>.01&&o<.99||(Q(this,lc,o*n),$(this,Oc,kc).call(this,X(this,lc)))},Oc=new WeakSet,kc=function(t){this.dispatchEvent(new v.CustomEvent(e.MEDIA_PREVIEW_REQUEST,{composed:!0,bubbles:!0,detail:t}))},Ac=new WeakSet,jc=function(){X(this,sc).stop();let t=Fc(this);this.dispatchEvent(new v.CustomEvent(e.MEDIA_SEEK_REQUEST,{composed:!0,bubbles:!0,detail:t}))},Ic.shadowRootOptions={mode:`open`},Ic.getContainerTemplateHTML=Nc,v.customElements.get(`media-time-range`)||v.customElements.define(`media-time-range`,Ic);var Lc=(e,t,n)=>{if(!t.has(e))throw TypeError(`Cannot `+n)},Rc=(e,t,n)=>(Lc(e,t,`read from private field`),n?n.call(e):t.get(e)),zc=(e,t,n)=>{if(t.has(e))throw TypeError(`Cannot add the same private member more than once`);t instanceof WeakSet?t.add(e):t.set(e,n)},Bc,Vc=1,Hc=e=>e.mediaMuted?0:e.mediaVolume,Uc=e=>`${Math.round(e*100)}%`,Wc=class extends zi{constructor(){super(...arguments),zc(this,Bc,()=>{let t=this.range.value,n=new v.CustomEvent(e.MEDIA_VOLUME_REQUEST,{composed:!0,bubbles:!0,detail:t});this.dispatchEvent(n)})}static get observedAttributes(){return[...super.observedAttributes,i.MEDIA_VOLUME,i.MEDIA_MUTED,i.MEDIA_VOLUME_UNAVAILABLE]}connectedCallback(){super.connectedCallback(),this.range.setAttribute(`aria-label`,_(`volume`)),this.range.addEventListener(`input`,Rc(this,Bc))}disconnectedCallback(){this.range.removeEventListener(`input`,Rc(this,Bc)),super.disconnectedCallback()}attributeChangedCallback(e,t,n){super.attributeChangedCallback(e,t,n),(e===i.MEDIA_VOLUME||e===i.MEDIA_MUTED)&&(this.range.valueAsNumber=Hc(this),this.range.setAttribute(`aria-valuetext`,Uc(this.range.valueAsNumber)),this.updateBar())}get mediaVolume(){return S(this,i.MEDIA_VOLUME,Vc)}set mediaVolume(e){C(this,i.MEDIA_VOLUME,e)}get mediaMuted(){return w(this,i.MEDIA_MUTED)}set mediaMuted(e){T(this,i.MEDIA_MUTED,e)}get mediaVolumeUnavailable(){return E(this,i.MEDIA_VOLUME_UNAVAILABLE)}set mediaVolumeUnavailable(e){D(this,i.MEDIA_VOLUME_UNAVAILABLE,e)}};Bc=new WeakMap,v.customElements.get(`media-volume-range`)||v.customElements.define(`media-volume-range`,Wc);function Gc(e){return`
      <style>
        :host {
          min-width: 4ch;
          padding: var(--media-button-padding, var(--media-control-padding, 10px 5px));
          width: 100%;
          display: grid;
          grid-template-columns: 1fr auto;
          gap: 1rem;
          font-weight: var(--media-button-font-weight, normal);
        }

        #checked-indicator {
          display: none;
        }

        :host([${i.MEDIA_LOOP}]) #checked-indicator {
          display: block;
        }
      </style>
      
      <span id="icon">
     </span>

      <div id="checked-indicator">
        <svg aria-hidden="true" viewBox="0 1 24 24" part="checked-indicator indicator">
          <path d="m10 15.17 9.193-9.191 1.414 1.414-10.606 10.606-6.364-6.364 1.414-1.414 4.95 4.95Z"/>
        </svg>
      </div>
    `}function Kc(){return _(`Loop`)}var qc=class extends H{constructor(){super(...arguments),this.container=null}static get observedAttributes(){return[...super.observedAttributes,i.MEDIA_LOOP]}connectedCallback(){super.connectedCallback(),this.container=this.shadowRoot?.querySelector(`#icon`)||null,this.container&&(this.container.textContent=_(`Loop`))}attributeChangedCallback(e,t,n){super.attributeChangedCallback(e,t,n),e===i.MEDIA_LOOP&&this.container&&this.setAttribute(`aria-checked`,this.mediaLoop?`true`:`false`)}get mediaLoop(){return w(this,i.MEDIA_LOOP)}set mediaLoop(e){T(this,i.MEDIA_LOOP,e)}handleClick(){let t=!this.mediaLoop,n=new v.CustomEvent(e.MEDIA_LOOP_REQUEST,{composed:!0,bubbles:!0,detail:t});this.dispatchEvent(n)}};qc.getSlotTemplateHTML=Gc,qc.getTooltipContentHTML=Kc,v.customElements.get(`media-loop-button`)||v.customElements.define(`media-loop-button`,qc),ae(`zh-CN`,{"Start airplay":`开始 AirPlay`,"Stop airplay":`停止 AirPlay`,Audio:`音频`,Captions:`字幕`,"Enable captions":`开启字幕`,"Disable captions":`关闭字幕`,"Start casting":`开始投屏`,"Stop casting":`停止投屏`,"Enter fullscreen mode":`进入全屏`,"Exit fullscreen mode":`退出全屏`,Mute:`静音`,Unmute:`恢复音量`,Loop:`循环播放`,"Enter picture in picture mode":`开启画中画`,"Exit picture in picture mode":`关闭画中画`,Play:`播放`,Pause:`暂停`,"Playback rate":`播放速度`,"Playback rate {playbackRate}":`播放速度：{playbackRate}`,Quality:`清晰度`,"Seek backward":`快退`,"Seek forward":`快进`,Settings:`设置`,Auto:`自动`,"audio player":`音频播放器`,"video player":`视频播放器`,volume:`音量`,seek:`跳转`,"closed captions":`隐藏式辅助字幕`,"current playback rate":`当前播放速度`,"playback time":`播放时间`,"media loading":`媒体加载中...`,settings:`设置`,"audio tracks":`音轨`,quality:`清晰度`,play:`播放`,pause:`暂停`,mute:`静音`,unmute:`恢复音量`,"chapter: {chapterName}":`章节: {chapterName}`,live:`直播`,Off:`关闭`,"start airplay":`开始 AirPlay`,"stop airplay":`停止 AirPlay`,"start casting":`开始投屏`,"stop casting":`停止投屏`,"enter fullscreen mode":`进入全屏`,"exit fullscreen mode":`退出全屏`,"enter picture in picture mode":`开启画中画`,"exit picture in picture mode":`关闭画中画`,"seek to live":`跳转至直播进度`,"playing live":`正在直播中`,"seek back {seekOffset} seconds":`快退 {seekOffset} 秒`,"seek forward {seekOffset} seconds":`快进 {seekOffset} 秒`,"Network Error":`网络错误`,"Decode Error":`解码失败`,"Source Not Supported":`不支持的媒体来源`,"Encryption Error":`加密错误`,"A network error caused the media download to fail.":`媒体下载失败，请检查网络连接。`,"A media error caused playback to be aborted. The media could be corrupt or your browser does not support this format.":`媒体错误导致播放中止。可能是文件损坏，或浏览器不支持该格式。`,"An unsupported error occurred. The server or network failed, or your browser does not support this format.":`发生未支持的错误，可能是服务器或网络故障，或浏览器不支持该格式。`,"The media is encrypted and there are no keys to decrypt it.":`媒体已加密，缺少解密密钥。`,hour:`小时`,hours:`小时`,minute:`分钟`,minutes:`分钟`,second:`秒`,seconds:`秒`,"{time} remaining":`剩余 {time}`,"{currentTime} of {totalTime}":`{currentTime} / {totalTime}`,"video not loaded, unknown time.":`视频未加载，时间未知。`});export{ie as t};