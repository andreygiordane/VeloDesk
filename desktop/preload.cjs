// SPDX-License-Identifier: AGPL-3.0-only
// Ponte mínima e explícita entre a UI (http://127.0.0.1) e o processo principal.
const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('velodesk', {
  getShellPrefs: () => ipcRenderer.invoke('shell-prefs:get'),
  setShellPrefs: (p) => ipcRenderer.invoke('shell-prefs:set', p),
  requestShare: () => ipcRenderer.invoke('share:request'),
  clip: { read: () => ipcRenderer.invoke('clip:read'), write: (t) => ipcRenderer.invoke('clip:write', t) },
  secrets: {
    get: (k) => ipcRenderer.invoke('secret:get', k),
    set: (k, v) => ipcRenderer.invoke('secret:set', k, v),
    del: (k) => ipcRenderer.invoke('secret:del', k),
    keys: () => ipcRenderer.invoke('secret:keys'),
  },
  overlay: { mark: (on) => ipcRenderer.invoke('overlay:mark', !!on), session: (on) => ipcRenderer.invoke('overlay:session', !!on), min: () => ipcRenderer.invoke('overlay:min'), invite: (on) => ipcRenderer.invoke('overlay:invite', !!on) },
  notify: (t, b) => ipcRenderer.invoke('notify', t, b),
  pickFolder: () => ipcRenderer.invoke('dialog:folder'),
  quit: () => ipcRenderer.invoke('app:quit'),
  win: {
    minimize: () => ipcRenderer.invoke('win:minimize'),
    toggleMaximize: () => ipcRenderer.invoke('win:toggle-maximize'),
    close: () => ipcRenderer.invoke('win:close'),
  },
});
