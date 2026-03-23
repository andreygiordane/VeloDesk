const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('electronAPI', {
    onInitHost: (callback) => ipcRenderer.on('init-host', (_event, value) => callback(value)),
    getHostConfig: () => ipcRenderer.send('get-host-config'),
    updatePassword: (newPass) => ipcRenderer.send('update-password', newPass),
    openViewer: (data) => ipcRenderer.send('open-viewer', data)
});
