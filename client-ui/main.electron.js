const { app, BrowserWindow, ipcMain } = require('electron');
const path = require('path');
const { spawn } = require('child_process');

let mainWindow;
let pythonProcess;

function createWindow() {
    mainWindow = new BrowserWindow({
        width: 1050,
        height: 750,
        backgroundColor: '#121212',
        title: 'VeloDesk',
        webPreferences: {
            nodeIntegration: false,
            contextIsolation: true,
            preload: path.join(__dirname, 'preload.js')
        }
    });

    mainWindow.loadURL('http://localhost:5173'); // Using Dev server for hot reload
    mainWindow.setMenuBarVisibility(false);
}

function startPythonMotor() {
    const pythonPath = path.join(__dirname, '..', 'capture-motor', 'venv', 'Scripts', 'python.exe');
    const scriptPath = path.join(__dirname, '..', 'capture-motor', 'main.py');
    
    pythonProcess = spawn(pythonPath, [scriptPath]);
    
    pythonProcess.stdout.on('data', (data) => {
        const output = data.toString();
        const initMatch = output.match(/##VELODESK:(.*)##/);
        if (initMatch && mainWindow) {
            try { mainWindow.webContents.send('init-host', JSON.parse(initMatch[1])); } catch (e) {}
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
ipcMain.on('update-password', (e, newPass) => {
    if (pythonProcess) pythonProcess.stdin.write(JSON.stringify({ action: "update-password", password: newPass }) + "\n");
});
ipcMain.on('accept-connection', (e, viewerId) => {
    if (pythonProcess) pythonProcess.stdin.write(JSON.stringify({ action: "accept-connection", viewerId }) + "\n");
});
ipcMain.on('reject-connection', (e, viewerId) => {
    if (pythonProcess) pythonProcess.stdin.write(JSON.stringify({ action: "reject-connection", viewerId }) + "\n");
});
