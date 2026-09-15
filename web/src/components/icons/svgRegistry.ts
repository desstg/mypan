/** 固定调色 SVG 片段；key 须受控，勿用用户输入作 key。 */
const NS = ' xmlns="http://www.w3.org/2000/svg"'

const SVG_MAP = {
  file: `<svg viewBox="0 0 24 24"${NS}><path fill="#F1F5F9" stroke="#94A3B8" stroke-width="1" d="M8 2h6l5 5v15H8a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1z"/><path fill="#CBD5E1" d="M14 2v5h5"/></svg>`,

  'trash-button': `<svg viewBox="0 0 24 24"${NS}><path fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" d="M4 7h16"/><path fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" d="M9 7V5.6c0-.9.7-1.6 1.6-1.6h2.8c.9 0 1.6.7 1.6 1.6V7"/><path fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" d="M7 7l.8 11c.1 1.1 1 2 2.1 2h4.2c1.1 0 2-.9 2.1-2L17 7"/><path fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" d="M10 10.5v5.5M14 10.5v5.5"/></svg>`,

  // 任务面板暂停/继续：线框风，与 trash-button 一致（勿走阿里彩色播放器图标）
  pause: `<svg viewBox="0 0 24 24"${NS}><path fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" d="M9 6.5v11M15 6.5v11"/></svg>`,
  play: `<svg viewBox="0 0 24 24"${NS}><path fill="none" stroke="currentColor" stroke-width="1.9" stroke-linejoin="round" stroke-linecap="round" d="M9 6.8v10.4L17.5 12 9 6.8z"/></svg>`,

  'confirm-trash': `<svg viewBox="0 0 448 512"${NS}><path fill="currentColor" d="M32 464a48 48 0 0 0 48 48h288a48 48 0 0 0 48-48V128H32v336zm96-208a16 16 0 0 1 32 0v208a16 16 0 0 1-32 0V256zm96 0a16 16 0 0 1 32 0v208a16 16 0 0 1-32 0V256zm96 0a16 16 0 0 1 32 0v208a16 16 0 0 1-32 0V256zM432 32H312l-9.4-18.7A24 24 0 0 0 281.1 0H166.9a24 24 0 0 0-21.4 12.3L136 32H16A16 16 0 0 0 0 48v32a16 16 0 0 0 16 16h416a16 16 0 0 0 16-16V48a16 16 0 0 0-16-16z"/></svg>`,

  'folder-plus': `<svg viewBox="0 0 24 24"${NS}><path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" d="M3 7.5A1.5 1.5 0 0 1 4.5 6h4l2 2h9A1.5 1.5 0 0 1 21 9.5v8A1.5 1.5 0 0 1 19.5 19h-15A1.5 1.5 0 0 1 3 17.5v-10z"/><path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" d="M12 11.5v4M10 13.5h4"/></svg>`,
  user: `<svg viewBox="0 0 24 24"${NS}><circle cx="12" cy="8" r="3.5" fill="#60A5FA"/><path fill="#3B82F6" d="M6 20v-1a4 4 0 0 1 4-4h4a4 4 0 0 1 4 4v1H6z"/></svg>`,

  monitor: `<svg viewBox="0 0 24 24"${NS}><rect x="4" y="4" width="16" height="11" rx="1.5" fill="#334155"/><rect x="6" y="6" width="12" height="7" rx="0.5" fill="#94A3B8"/><path fill="#475569" d="M9 18h6v2H9z"/><path fill="#64748B" d="M7 20h10v1H7z"/></svg>`,

  cloud: `<svg viewBox="0 0 24 24"${NS}><path fill="#7DD3FC" d="M6 17h12v1H6z"/><circle cx="8" cy="14" r="4" fill="#38BDF8"/><circle cx="12" cy="12" r="5" fill="#0EA5E9"/><circle cx="16" cy="14" r="4" fill="#38BDF8"/><circle cx="14" cy="16" r="3.5" fill="#7DD3FC"/></svg>`,

  relay: `<svg viewBox="0 0 24 24"${NS}><path fill="none" stroke="#2563EB" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" d="M13 2 5 14h6l-1 8 9-12h-6z"/></svg>`,

  'chevron-down': `<svg viewBox="0 0 24 24"${NS}><path fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" d="m6 9 6 6 6-6"/></svg>`,

  // 新建：加号（线框风，与 pause/play 一致）
  plus: `<svg viewBox="0 0 24 24"${NS}><path fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" d="M12 5v14M5 12h14"/></svg>`,
  // 收藏夹：标准正五角星（尖角朝正上；空心/实心两种状态）
  star: `<svg viewBox="0 0 24 24"${NS}><path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linejoin="round" d="M12 2.5l2.6 6.1 6.6.5-5 4.6 1.4 6.5L12 16.9l-5.6 3.3 1.4-6.5-5-4.6 6.6-.5z"/></svg>`,
  'star-solid': `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="M12 2.5l2.6 6.1 6.6.5-5 4.6 1.4 6.5L12 16.9l-5.6 3.3 1.4-6.5-5-4.6 6.6-.5z"/></svg>`,

  'help-circle': `<svg viewBox="0 0 24 24"${NS}><circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" stroke-width="1.8"/><circle cx="12" cy="8.25" r="0.9" fill="currentColor"/><path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" d="M12 11v5"/></svg>`,
  lock: `<svg viewBox="0 0 24 24"${NS}><path fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" d="M8 10V7.7a4 4 0 1 1 8 0V10"/><rect x="5.5" y="10" width="13" height="10" rx="2.4" fill="none" stroke="currentColor" stroke-width="2"/><path fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" d="M12 14v2.2"/><circle cx="12" cy="13.2" r="0.9" fill="currentColor"/></svg>`,
  'lock-open': `<svg viewBox="0 0 24 24"${NS}><path fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" d="M8 10V8a4 4 0 0 1 4-4c1.5 0 2.8.8 3.5 2"/><rect x="5.5" y="10" width="13" height="10" rx="2.4" fill="none" stroke="currentColor" stroke-width="2"/><path fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" d="M12 14v2.2"/><circle cx="12" cy="13.2" r="0.9" fill="currentColor"/></svg>`,

  settings: `<svg viewBox="0 0 24 24"${NS}><path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" d="M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z"/><path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z"/></svg>`,

  copy: `<svg viewBox="0 0 24 24"${NS}><rect x="8" y="8" width="12" height="12" rx="2" fill="#E0E7FF"/><rect x="8" y="8" width="12" height="12" rx="2" fill="none" stroke="#6366F1" stroke-width="1.2"/><path fill="#818CF8" d="M6 4h9.5L18 6.5V18a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1z"/></svg>`,

  'fa-file-alt': `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="M7 2h7l5 5v13a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2zm6 1.8V8h4.2L13 3.8zM8.5 11h7v1.8h-7zm0 3.6h7v1.8h-7zm0 3.6h4.6V20H8.5z"/></svg>`,

  'fa-exclamation-triangle': `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="M12 3.4a1.2 1.2 0 0 1 1.05.6l8.12 14.2A1.2 1.2 0 0 1 20.12 20H3.88a1.2 1.2 0 0 1-1.05-1.8L10.95 4a1.2 1.2 0 0 1 1.05-.6zm-1 5.1v5.9h2V8.5h-2zm1 9.2a1.35 1.35 0 1 0 0-2.7 1.35 1.35 0 0 0 0 2.7z"/></svg>`,

  'fa-cubes': `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="m11.2 2.6-4.8 2.8v5.5l4.8 2.8 4.8-2.8V5.4l-4.8-2.8zm-3 4 3-1.7 3 1.7-3 1.7-3-1.7zm-1 5.6-4.8 2.7v5.5l4.8 2.8 4.8-2.8V15l-4.8-2.8zm9.6 0L12 15v5.5l4.8 2.8 4.8-2.8v-5.5l-4.8-2.8z"/></svg>`,

  'fa-list': `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="M7 5.5A1.5 1.5 0 1 1 4 5.5a1.5 1.5 0 0 1 3 0zm0 6A1.5 1.5 0 1 1 4 11.5a1.5 1.5 0 0 1 3 0zm0 6A1.5 1.5 0 1 1 4 17.5a1.5 1.5 0 0 1 3 0zM9.5 4.5H20v2H9.5zm0 6H20v2H9.5zm0 6H20v2H9.5z"/></svg>`,

  'fa-folder': `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="M3.5 5h6.2l1.7 2H20a1.5 1.5 0 0 1 1.5 1.5v8.8A1.7 1.7 0 0 1 19.8 19H4.2a1.7 1.7 0 0 1-1.7-1.7V6.5A1.5 1.5 0 0 1 4 5h-.5z"/></svg>`,

  'fa-play': `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="M8 6.8c0-1 1.1-1.6 2-1.1l8 4.7c.9.5.9 1.8 0 2.3l-8 4.7c-.9.5-2 .0-2-1.1V6.8z"/></svg>`,

  'fa-pause': `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="M7 5.5h3.5a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H7a1 1 0 0 1-1-1v-11a1 1 0 0 1 1-1zm6.5 0H17a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1h-3.5a1 1 0 0 1-1-1v-11a1 1 0 0 1 1-1z"/></svg>`,

  'fa-link': `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="M10.6 13.4a1 1 0 0 1 0-1.4l2.8-2.8a3.5 3.5 0 1 1 5 5l-1.9 1.9a3.5 3.5 0 0 1-5 0 1 1 0 1 1 1.4-1.4 1.5 1.5 0 0 0 2.1 0l1.9-1.9a1.5 1.5 0 1 0-2.1-2.1L12 13.4a1 1 0 0 1-1.4 0zm2.8-2.8a1 1 0 0 1 0 1.4l-2.8 2.8a3.5 3.5 0 1 1-5-5l1.9-1.9a3.5 3.5 0 0 1 5 0 1 1 0 1 1-1.4 1.4 1.5 1.5 0 0 0-2.1 0l-1.9 1.9a1.5 1.5 0 1 0 2.1 2.1l2.8-2.8a1 1 0 0 1 1.4 0z"/></svg>`,

  'fa-database': `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="M12 4c-4.4 0-8 1.4-8 3.2v9.6C4 18.6 7.6 20 12 20s8-1.4 8-3.2V7.2C20 5.4 16.4 4 12 4zm0 2c3.8 0 6 .9 6 1.2s-2.2 1.2-6 1.2-6-.9-6-1.2S8.2 6 12 6zm0 5.2c2.3 0 4.4-.3 6-1v2.1c0 .3-2.2 1.2-6 1.2s-6-.9-6-1.2v-2.1c1.6.7 3.7 1 6 1zm0 5c2.3 0 4.4-.3 6-1v1.6c0 .3-2.2 1.2-6 1.2s-6-.9-6-1.2v-1.6c1.6.7 3.7 1 6 1z"/></svg>`,

  'fa-hdd': `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="M4.5 6h15a2.5 2.5 0 0 1 2.4 1.8l1.5 5A3 3 0 0 1 20.5 17h-17A3 3 0 0 1 .6 12.8l1.5-5A2.5 2.5 0 0 1 4.5 6zm0 2a.5.5 0 0 0-.5.4l-1.5 5A1 1 0 0 0 3.5 15h17a1 1 0 0 0 1-1.6l-1.5-5a.5.5 0 0 0-.5-.4h-15zm11.8 4.9a1.4 1.4 0 1 1 0 2.8 1.4 1.4 0 0 1 0-2.8zm3.7 0a1.4 1.4 0 1 1 0 2.8 1.4 1.4 0 0 1 0-2.8zM6.2 10h6.5a1 1 0 1 1 0 2H6.2a1 1 0 1 1 0-2z"/></svg>`,

  'fa-bullseye': `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="M12 3a9 9 0 1 1 0 18 9 9 0 0 1 0-18zm0 2.2a6.8 6.8 0 1 0 0 13.6 6.8 6.8 0 0 0 0-13.6zm0 3a3.8 3.8 0 1 1 0 7.6 3.8 3.8 0 0 1 0-7.6zm0 2.2a1.6 1.6 0 1 0 0 3.2 1.6 1.6 0 0 0 0-3.2z"/></svg>`,

  'fa-sync-alt': `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="M21.3 4.3a1 1 0 0 0-1.4 0l-1.1 1.1A8.95 8.95 0 0 0 4.2 8.1a1 1 0 1 0 1.9.6 7 7 0 0 1 11.2-2L15.5 8.5a1 1 0 0 0 .7 1.7H21a1 1 0 0 0 1-1V5a1 1 0 0 0-.7-.7z"/><path fill="currentColor" d="M19.8 15.3a1 1 0 0 0-1.3.6 7 7 0 0 1-11.3 2l1.8-1.8a1 1 0 0 0-.7-1.7H3a1 1 0 0 0-1 1v4.2a1 1 0 0 0 1.7.7l1.1-1.1A8.95 8.95 0 0 0 19.8 15.3z"/></svg>`,

  'fa-undo-alt': `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="M7.4 7H4.2a1 1 0 0 1-1-1V2.8a1 1 0 1 1 2 0V5h2.2a9 9 0 1 1-1.6 11.3 1 1 0 1 1 1.7-1A7 7 0 1 0 8.3 7z"/></svg>`,

  'fa-eraser': `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="m15.6 4.3 4.1 4.1a2 2 0 0 1 0 2.8l-6.8 6.8a2 2 0 0 1-1.4.6H5.8a2 2 0 0 1-1.4-.6L2.3 16a2 2 0 0 1 0-2.8l8.9-8.9a3 3 0 0 1 4.4 0zm-8.1 12.2h3.6l5.2-5.2-3.6-3.6-7.1 7.1 1.9 1.7zM14 19h7a1 1 0 1 1 0 2h-7a1 1 0 1 1 0-2z"/></svg>`,

  'fa-trash-alt': `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="M9 3h6a2 2 0 0 1 2 2v1h3a1 1 0 1 1 0 2h-1l-.8 10.5A2.5 2.5 0 0 1 15.7 21H8.3a2.5 2.5 0 0 1-2.5-2.5L5 8H4a1 1 0 1 1 0-2h3V5a2 2 0 0 1 2-2zm0 3h6V5H9v1zm-.8 3a1 1 0 0 1 1 1l.3 7a1 1 0 1 1-2 .1l-.3-7a1 1 0 0 1 1-1zm7.6 0a1 1 0 0 1 1 1l-.3 7a1 1 0 1 1-2-.1l.3-7a1 1 0 0 1 1-1zM12 9a1 1 0 0 1 1 1v7a1 1 0 1 1-2 0v-7a1 1 0 0 1 1-1z"/></svg>`,

  // 官网 / GitHub / B 站：footer badge 用，currentColor 适配深色 label
  globe: `<svg viewBox="0 0 24 24"${NS}><circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" stroke-width="1.8"/><ellipse cx="12" cy="12" rx="3.8" ry="9" fill="none" stroke="currentColor" stroke-width="1.5"/><path fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" d="M3.2 12h17.6M4.8 7.2h14.4M4.8 16.8h14.4"/></svg>`,
  github: `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="M12 2C6.48 2 2 6.58 2 12.26c0 4.52 2.87 8.35 6.84 9.7.5.1.68-.22.68-.48 0-.24-.01-.87-.01-1.7-2.78.62-3.37-1.37-3.37-1.37-.45-1.18-1.11-1.5-1.11-1.5-.91-.64.07-.63.07-.63 1 .07 1.53 1.06 1.53 1.06.89 1.56 2.34 1.11 2.91.85.09-.66.35-1.11.63-1.37-2.22-.26-4.56-1.14-4.56-5.07 0-1.12.39-2.03 1.03-2.75-.1-.26-.45-1.3.1-2.7 0 0 .84-.27 2.75 1.05A9.3 9.3 0 0 1 12 6.84c.85 0 1.71.12 2.51.35 1.91-1.32 2.75-1.05 2.75-1.05.55 1.4.2 2.44.1 2.7.64.72 1.03 1.63 1.03 2.75 0 3.94-2.34 4.8-4.57 5.06.36.32.68.94.68 1.9 0 1.37-.01 2.47-.01 2.81 0 .26.18.59.69.48A10.03 10.03 0 0 0 22 12.26C22 6.58 17.52 2 12 2z"/></svg>`,
  bilibili: `<svg viewBox="0 0 24 24"${NS}><path fill="currentColor" d="M17.813 4.653h.854c1.51.054 2.765.578 3.63 1.533.908 1.006 1.254 2.228 1.253 3.825v7.421c0 1.59-.344 2.81-1.253 3.815-.864.955-2.12 1.48-3.63 1.533H5.333c-1.51-.053-2.766-.577-3.63-1.533C.791 20.239.436 19.017.436 17.432V9.986c0-1.597.355-2.819 1.253-3.825.864-.955 2.12-1.48 3.63-1.533h.854l.854-2.214a.45.45 0 0 1 .54-.28l2.873.854a.45.45 0 0 0 .27 0l2.873-.854a.45.45 0 0 1 .54.28l.854 2.214zm-12.4 3.2c-.588 0-1.067.48-1.067 1.067v8.533c0 .588.48 1.067 1.067 1.067h12.8c.588 0 1.067-.48 1.067-1.067V8.92c0-.588-.48-1.067-1.067-1.067H5.413zm2.133 2.133h1.6v4.267h-1.6V9.986zm6.4 0h1.6v4.267h-1.6V9.986z"/></svg>`,

  "sign-in": `<svg viewBox="0 0 24 24"${NS}><circle cx="12" cy="8" r="3.2" fill="none" stroke="currentColor" stroke-width="1.8"/><path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" d="M6.2 19.2c.9-3.2 3-4.8 5.8-4.8s4.9 1.6 5.8 4.8"/></svg>`,
  "sign-out": `<svg viewBox="0 0 24 24"${NS}><path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" d="M10 4H6.5A1.5 1.5 0 0 0 5 5.5v13A1.5 1.5 0 0 0 6.5 20H10"/><path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" d="M10 12h9"/><path fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" d="m15.5 8.5 3.5 3.5-3.5 3.5"/></svg>`
}

export function getSvg(name: string): string {
  return SVG_MAP[name as keyof typeof SVG_MAP] || SVG_MAP.file;
}
