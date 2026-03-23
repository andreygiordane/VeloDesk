const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('electronAPI', {
    onInitHost: (callback) => ipcRenderer.on('init-host', (_event, value) => callback(value)),
    updatePassword: (newPass) => ipcRenderer.send('update-password', newPass),
    
    onConnectionRequest: (callback) => ipcRenderer.on('connection-request', (_event, value) => callback(value)),
    acceptConnection: (viewerId) => ipcRenderer.send('accept-connection', viewerId),
    rejectConnection: (viewerId) => ipcRenderer.send('reject-connection', viewerId)
});
