const { app, BrowserWindow, ipcMain } = require('electron');
const path = require('path');
const { spawn } = require('child_process');

let mainWindow;
let pythonProcess;
let hostConfigLocal = null;

function createWindow() {
    mainWindow = new BrowserWindow({
        width: 1050,
        height: 750,
        backgroundColor: '#121212',
        title: 'VeloDesk',
        webPreferences: {
            nodeIntegration: false,
            contextIsolation: true,
            preload: path.join(__dirname, 'preload.cjs')
        }
    });

    mainWindow.loadURL('http://127.0.0.1:5100');
    mainWindow.setMenuBarVisibility(false);
}

function startPythonMotor() {
    const pythonPath = path.join(__dirname, '..', 'capture-motor', 'venv', 'Scripts', 'python.exe');
    const scriptPath = path.join(__dirname, '..', 'capture-motor', 'main.py');
    
    pythonProcess = spawn(pythonPath, [scriptPath]);
    
    pythonProcess.stdout.on('data', (data) => {
        const output = data.toString();
        const initMatch = output.match(/##VELODESK:(.*)##/);
        if (initMatch) {
            try { 
                hostConfigLocal = JSON.parse(initMatch[1]);
                if (mainWindow) mainWindow.webContents.send('init-host', hostConfigLocal); 
            } catch (e) {}
        }
        
        const reqMatch = output.match(/##VELODESK_REQUEST:(.*)##/);
        if (reqMatch && mainWindow) {
            try { mainWindow.webContents.send('connection-request', JSON.parse(reqMatch[1])); } catch (e) {}
        }
    });
    
    pythonProcess.stderr.on('data', (data) => console.error(`[PYTHON] ${data.toString()}`));
}

app.whenReady().then(() => {
    createWindow();
    startPythonMotor();
});

app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit(); });
app.on('will-quit', () => { if (pythonProcess) pythonProcess.kill(); });

// IPC from UI to Python
ipcMain.on('get-host-config', (e) => {
    if (hostConfigLocal) e.reply('init-host', hostConfigLocal);
});
ipcMain.on('update-password', (e, newPass) => {
    if (pythonProcess) pythonProcess.stdin.write(JSON.stringify({ action: "update-password", password: newPass }) + "\n");
});

// ABRIR NOVA JANELA DE VISUALIZAÇÃO
ipcMain.on('open-viewer', (e, data) => {
    const { remoteId, password } = data;
    
    let viewerWindow = new BrowserWindow({
        width: 1280,
        height: 720,
        backgroundColor: '#000',
        webPreferences: {
            nodeIntegration: false,
            contextIsolation: true
        }
    });

    // In DEV, we use the Vite dev server URL but pointing to viewer.html
    // Or we can just use the file path if it's simpler
    const viewerUrl = `http://127.0.0.1:5100/viewer.html?id=${remoteId}&pass=${password}`;
    viewerWindow.loadURL(viewerUrl);
    viewerWindow.setMenuBarVisibility(false);
});
