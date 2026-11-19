// SPDX-License-Identifier: AGPL-3.0-only
﻿// Casca do VeloDesk: abre a janela, inicia o nÃºcleo Go e mantÃ©m o app na bandeja.
// Toda a lÃ³gica (sinalizaÃ§Ã£o, WebRTC, entrada remota) fica no nÃºcleo e na UI web.
const { app, BrowserWindow, Tray, Menu, session, desktopCapturer, shell, nativeImage, ipcMain, clipboard, dialog, safeStorage, screen, Notification } = require('electron');
const { spawn, execFileSync } = require('child_process');
const path = require('path');
const http = require('http');
const fs = require('fs');
const os = require('os');

const PORT = Number(process.env.VELODESK_PORT) || 21120; // VELODESK_PORT permite abrir uma segunda instÃ¢ncia para testes
const URL = `http://127.0.0.1:${PORT}`;
const exeName = process.platform === 'win32' ? 'velodesk.exe' : 'velodesk';

// Ãcone do app (Windows prefere .ico) e identidade do app: sem o AppUserModelID o Windows mostra o Ã­cone do Electron na barra de tarefas.
app.setAppUserModelId('com.velodesk.app');
for (const f of ['disable-renderer-backgrounding', 'disable-background-timer-throttling', 'disable-backgrounding-occluded-windows']) app.commandLine.appendSwitch(f);
const ICON_PATH = path.join(__dirname, process.platform === 'win32' && fs.existsSync(path.join(__dirname, 'icon.ico')) ? 'icon.ico' : 'icon.png');

let win, tray, core, quitting = false;
const startHidden = process.argv.includes('--hidden');

// ---- preferÃªncias da casca (iniciar com o sistema / bandeja) ----
const shellFile = () => path.join(app.getPath('userData'), 'shell.json');
function readShell() { try { return { closeToTray: true, ...JSON.parse(fs.readFileSync(shellFile(), 'utf8')) }; } catch { return { closeToTray: true }; } }
function writeShell(p) { fs.mkdirSync(path.dirname(shellFile()), { recursive: true }); fs.writeFileSync(shellFile(), JSON.stringify(p)); }

const linuxAutostartFile = () => path.join(os.homedir(), '.config', 'autostart', 'velodesk.desktop');
function getAutostart() {
  if (process.platform === 'linux') return fs.existsSync(linuxAutostartFile());
  return app.getLoginItemSettings().openAtLogin;
}
function setAutostart(on) {
  if (!app.isPackaged) throw new Error('DisponÃ­vel apenas no app instalado (nÃ£o no modo de desenvolvimento).');
  if (process.platform === 'linux') {
    const f = linuxAutostartFile();
    if (!on) { try { fs.unlinkSync(f); } catch {} return; }
    const exe = process.env.APPIMAGE || process.execPath;
    fs.mkdirSync(path.dirname(f), { recursive: true });
    fs.writeFileSync(f, `[Desktop Entry]\nType=Application\nName=VeloDesk\nExec="${exe}" --hidden\nX-GNOME-Autostart-enabled=true\n`);
  } else {
    app.setLoginItemSettings({ openAtLogin: on, args: ['--hidden'] });
  }
}

ipcMain.handle('shell-prefs:get', () => ({ autostart: getAutostart(), closeToTray: readShell().closeToTray }));
ipcMain.handle('shell-prefs:set', (_e, p) => {
  try {
    writeShell({ ...readShell(), closeToTray: !!p.closeToTray });
    if (!!p.autostart !== getAutostart()) setAutostart(!!p.autostart);
    return { ok: true };
  } catch (e) { return { error: e.message }; }
});
// Ãrea de transferÃªncia (texto): lida no processo principal para funcionar mesmo com a janela em segundo plano.
ipcMain.handle('clip:read', () => clipboard.readText());
ipcMain.handle('clip:write', (_e, t) => { if (typeof t === 'string' && t.length < 200000) clipboard.writeText(t); });
// Cofre de segredos: valores cifrados pelo sistema (DPAPI no Windows, Keychain no macOS, libsecret no Linux).
const secretsFile = () => path.join(app.getPath('userData'), 'secrets.json');
const readSecrets = () => { try { return JSON.parse(fs.readFileSync(secretsFile(), 'utf8')); } catch { return {}; } };
const writeSecrets = (o) => { fs.mkdirSync(path.dirname(secretsFile()), { recursive: true }); fs.writeFileSync(secretsFile(), JSON.stringify(o), { mode: 0o600 }); };
const okKey = (k) => typeof k === 'string' && /^[\w:.-]{1,80}$/.test(k);
ipcMain.handle('secret:set', (_e, k, v) => {
  if (!okKey(k) || typeof v !== 'string' || v.length > 4096) throw new Error('segredo invÃ¡lido');
  if (!safeStorage.isEncryptionAvailable()) throw new Error('o sistema nÃ£o oferece cofre de segredos');
  const o = readSecrets(); o[k] = safeStorage.encryptString(v).toString('base64'); writeSecrets(o);
});
ipcMain.handle('secret:get', (_e, k) => {
  if (!okKey(k)) return null;
  const b = readSecrets()[k]; if (!b) return null;
  try { return safeStorage.decryptString(Buffer.from(b, 'base64')); } catch { return null; }
});
ipcMain.handle('secret:del', (_e, k) => { if (!okKey(k)) return; const o = readSecrets(); delete o[k]; writeSecrets(o); });
ipcMain.handle('secret:keys', () => Object.keys(readSecrets()));
ipcMain.handle('dialog:folder', async () => {
  const r = await dialog.showOpenDialog(win, { properties: ['openDirectory', 'createDirectory'] });
  return r.canceled ? null : r.filePaths[0];
});
ipcMain.handle('app:quit', () => { quitting = true; app.quit(); });
// Janelas flutuantes do computador controlado: faixa "sendo acessado" (sempre por cima) e painel de chat.
let markWin = null, sesWin = null;
const wprefs = () => ({ nodeIntegration: false, contextIsolation: true, sandbox: true });
function hardenOverlay(o, page) {
  o.setMenuBarVisibility(false);
  o.loadURL(`${URL}/overlay.html?m=${page}`);
  o.webContents.on('will-navigate', (e) => e.preventDefault());
  o.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
}
// Marca discreta no canto da tela (como a do AnyDesk): sempre visível, não bloqueia nenhum clique.
function mkMark() {
  const { workArea } = screen.getPrimaryDisplay(); const w = 560, h = 36;
  const o = new BrowserWindow({
    width: w, height: h, x: workArea.x + workArea.width - w, y: workArea.y + workArea.height - h,
    frame: false, transparent: true, resizable: false, skipTaskbar: true, alwaysOnTop: true, focusable: false, show: false, hasShadow: false,
    title: 'VeloDesk', icon: ICON_PATH, webPreferences: wprefs(),
  });
  o.setAlwaysOnTop(true, 'screen-saver'); o.setIgnoreMouseEvents(true);
  hardenOverlay(o, 'mark');
  o.once('ready-to-show', () => o.showInactive());
  o.on('closed', () => { markWin = null; });
  return o;
}
// Janela da sessão (perfil, permissões ao vivo, chat, arquivos e Desconectar): janela comum, pode ser minimizada.
// Começa compacta no canto inferior direito (acima da marca d'água) para não cobrir a tela de quem controla; "full" mostra tudo.
function sessionBounds(kind) {
  const { workArea } = screen.getPrimaryDisplay();
  const w = kind === 'full' ? 860 : 400, h = kind === 'full' ? 620 : 238;
  return kind === 'full'
    ? { width: w, height: h, x: Math.round(workArea.x + workArea.width - w - 48), y: Math.round(workArea.y + (workArea.height - h) / 2) }
    : { width: w, height: h, x: Math.round(workArea.x + workArea.width - w - 16), y: Math.round(workArea.y + workArea.height - h - 46) };
}
function mkSession() {
  const o = new BrowserWindow({
    ...sessionBounds('compact'),
    frame: false, transparent: true, resizable: false, show: false, hasShadow: false, title: 'VeloDesk · Sessão remota', icon: ICON_PATH, webPreferences: wprefs(),
  });
  hardenOverlay(o, 'session');
  o.once('ready-to-show', () => o.showInactive());
  o.on('closed', () => { sesWin = null; });
  return o;
}
ipcMain.handle('overlay:mark', (_e, on) => { if (on) { if (!markWin) markWin = mkMark(); } else if (markWin) markWin.close(); });
ipcMain.handle('overlay:session', (_e, on) => {
  if (on) { if (!sesWin) sesWin = mkSession(); else { if (sesWin.isMinimized()) sesWin.restore(); sesWin.showInactive(); } }
  else if (sesWin) sesWin.close();
});
ipcMain.handle('overlay:min', () => { if (sesWin) sesWin.minimize(); });
ipcMain.handle('overlay:size', (_e, kind) => { if (sesWin) sesWin.setBounds(sessionBounds(kind === 'full' ? 'full' : 'compact')); });
// Janela de aceite de convite: aparece no centro da tela, sempre por cima, mesmo com o app minimizado na bandeja.
let invWin = null;
function mkInvite() {
  const { workArea } = screen.getPrimaryDisplay(); const w = 820, h = 690;
  const o = new BrowserWindow({
    width: w, height: h, x: Math.round(workArea.x + (workArea.width - w) / 2), y: Math.round(workArea.y + (workArea.height - h) / 2),
    frame: false, transparent: true, resizable: false, alwaysOnTop: true, show: false, hasShadow: false, title: 'VeloDesk · Pedido de conexão', icon: ICON_PATH,
    webPreferences: { nodeIntegration: false, contextIsolation: true, sandbox: true },
  });
  o.setAlwaysOnTop(true, 'screen-saver'); o.setMenuBarVisibility(false);
  o.loadURL(`${URL}/overlay.html?m=invite`);
  o.webContents.on('will-navigate', (e) => e.preventDefault());
  o.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  o.once('ready-to-show', () => { o.show(); o.focus(); o.flashFrame(true); });
  o.on('closed', () => { invWin = null; });
  return o;
}
ipcMain.handle('overlay:invite', (_e, on) => { if (on) { if (invWin) { invWin.show(); invWin.focus(); } else invWin = mkInvite(); } else if (invWin) invWin.close(); });
ipcMain.handle('notify', (_e, t, b) => { if (Notification.isSupported()) new Notification({ title: String(t).slice(0, 80), body: String(b).slice(0, 200), icon: ICON_PATH }).show(); });
// O gesto de usuÃ¡rio vem do processo principal, para o acesso sem supervisÃ£o poder capturar a tela.
// Controles da janela (a janela nÃ£o tem moldura do sistema; a barra Ã© desenhada pela UI).
ipcMain.handle('win:minimize', () => win && win.minimize());
ipcMain.handle('win:toggle-maximize', () => { if (!win) return; win.isMaximized() ? win.unmaximize() : win.maximize(); });
ipcMain.handle('win:close', () => win && win.close()); // respeita "manter na bandeja" via o handler 'close'
ipcMain.handle('share:request', () => win.webContents.executeJavaScript('window.__doShare()', true));

function bundledCore() {
  if (app.isPackaged) return path.join(process.resourcesPath, exeName);
  const so = process.platform === 'win32' ? 'win' : process.platform === 'darwin' ? 'mac-' + process.arch : 'linux';
  return path.join(__dirname, 'resources', so, exeName); // gerado por `npm run core`
}

// ---- atualizaÃ§Ãµes: o nÃºcleo baixa versÃµes assinadas para <appData>/velodesk/updates ----
const updatesDir = () => path.join(app.getPath('appData'), 'velodesk', 'updates');
const semver = (v) => String(v).replace(/^v/, '').split('.').map((n) => parseInt(n, 10) || 0);
const isNewer = (a, b) => { const x = semver(a), y = semver(b); for (let i = 0; i < Math.max(x.length, y.length); i++) { const d = (x[i] || 0) - (y[i] || 0); if (d) return d > 0; } return false; };
function coreVersion(p) { try { return execFileSync(p, ['version'], { timeout: 5000, windowsHide: true }).toString().trim(); } catch { return '0.0.0'; } }

// Escolhe o nÃºcleo mais novo entre o embutido no instalador e os jÃ¡ baixados (e verificados).
function pickCore(dir = updatesDir(), bundled = bundledCore(), versionOf = coreVersion) {
  let best = { path: bundled, v: versionOf(bundled) };
  let names = [];
  try { names = fs.readdirSync(dir); } catch {}
  for (const n of names) {
    const m = /^velodesk-(\d+\.\d+\.\d+)(\.exe)?$/.exec(n);
    if (!m || (process.platform === 'win32') !== !!m[2]) continue;
    if (isNewer(m[1], best.v)) best = { path: path.join(dir, n), v: m[1] };
  }
  return best;
}

let restarts = [];
function startCore() {
  const pick = pickCore();
  console.log(`[nÃºcleo] ${pick.v} (${pick.path})`);
  core = spawn(pick.path, ['-no-open', '-addr', `127.0.0.1:${PORT}`], { stdio: ['ignore', 'inherit', 'inherit'], windowsHide: true, env: { ...process.env, VELODESK_SUPERVISED: '1' } });
  core.on('exit', async (code) => {
    core = null;
    if (quitting) return;
    // 42 = o nÃºcleo pediu reinÃ­cio para aplicar uma atualizaÃ§Ã£o; qualquer outra saÃ­da Ã© uma falha.
    const now = Date.now(); restarts = restarts.filter((t) => now - t < 60000);
    if (code !== 42 && restarts.length >= 5) { console.error('[nÃºcleo] caiu 5 vezes em 1 minuto; nÃ£o vou reiniciar de novo.'); return; }
    restarts.push(now);
    console.error(`[nÃºcleo] encerrou (cÃ³digo ${code}); reiniciandoâ€¦`);
    setTimeout(async () => {
      startCore();
      try { await waitForCore(); if (win) win.webContents.reloadIgnoringCache(); } catch (e) { console.error(e.message); }
    }, code === 42 ? 200 : 1500);
  });
}

function waitForCore(tries = 60) {
  return new Promise((resolve, reject) => {
    const probe = () => {
      http.get(URL, (res) => { res.resume(); resolve(); })
        .on('error', () => (--tries > 0 ? setTimeout(probe, 250) : reject(new Error('nÃºcleo nÃ£o respondeu'))));
    };
    probe();
  });
}

function createWindow(show = true) {
  win = new BrowserWindow({
    show,
    width: 1200, height: 780, minWidth: 760, minHeight: 600,
    backgroundColor: '#0a0910', title: 'VeloDesk', autoHideMenuBar: true, frame: false,
    icon: ICON_PATH,
    webPreferences: { nodeIntegration: false, contextIsolation: true, sandbox: true, backgroundThrottling: false, preload: path.join(__dirname, 'preload.cjs') },
  });
  win.setMenuBarVisibility(false);
  win.setIcon(nativeImage.createFromPath(ICON_PATH));
  win.loadURL(URL);
  // Nada de navegar para fora do app.
  win.webContents.setWindowOpenHandler(({ url }) => { if (/^https?:/.test(url)) shell.openExternal(url); return { action: 'deny' }; });
  win.webContents.on('will-navigate', (e, url) => { if (!url.startsWith(URL)) e.preventDefault(); });
  // Fechar a janela mantÃ©m o app ativo na bandeja (a mÃ¡quina continua acessÃ­vel).
  win.on('close', (e) => { if (!quitting && readShell().closeToTray) { e.preventDefault(); win.hide(); } else if (!quitting) { quitting = true; app.quit(); } });
}

function showWindow() { if (!win) createWindow(); win.show(); win.focus(); }

function createTray() {
  const img = nativeImage.createFromPath(path.join(__dirname, 'icon.png')).resize({ width: 32, height: 32 });
  tray = new Tray(img);
  tray.setToolTip('VeloDesk');
  tray.setContextMenu(Menu.buildFromTemplate([
    { label: 'Abrir VeloDesk', click: showWindow },
    { label: 'Mostrar sessão remota', click: () => { if (markWin) { if (!sesWin) sesWin = mkSession(); else { if (sesWin.isMinimized()) sesWin.restore(); sesWin.show(); } } } },
    { type: 'separator' },
    { label: 'Sair', click: () => { quitting = true; app.quit(); } },
  ]));
  tray.on('click', showWindow);
}

// Versão deste app (VELODESK_FAKE_VERSION só existe para testar a troca de versões).
const MY_VERSION = process.env.VELODESK_FAKE_VERSION || app.getVersion();
const relaunched = process.argv.includes('--relaunched');
if (!app.requestSingleInstanceLock({ version: MY_VERSION })) {
  // Já existe um VeloDesk aberto (talvez escondido na bandeja). Ele recebe a nossa versão e, se for mais antigo, se fecha;
  // então abrimos de novo uma única vez. Sem isso, instalar uma versão nova com a antiga na bandeja continuava mostrando a antiga.
  if (relaunched) app.quit();
  else setTimeout(() => { app.relaunch({ args: [...process.argv.slice(1), '--relaunched'] }); app.exit(0); }, 4000);
} else {
  app.on('second-instance', (_e, _argv, _cwd, data) => {
    if (data && data.version && isNewer(data.version, MY_VERSION)) { quitting = true; app.quit(); }   // versão mais nova chegou: cedo o lugar
    else showWindow();
  });
  app.whenReady().then(async () => {
    // getDisplayMedia no Electron precisa de um handler: compartilha a tela principal
    // sem exibir o seletor (o aceite jÃ¡ Ã© feito dentro do VeloDesk).
    session.defaultSession.setDisplayMediaRequestHandler((_req, cb) => {
      desktopCapturer.getSources({ types: ['screen'] })
        .then((sources) => cb(sources[0] ? { video: sources[0] } : {}))
        .catch(() => cb({}));
    });
    startCore();
    try { await waitForCore(); } catch (e) { console.error(e.message); }
    createWindow(!startHidden);
    createTray();
  });
  app.on('window-all-closed', () => { /* continua na bandeja */ });
  app.on('before-quit', () => { quitting = true; if (core) core.kill(); });
}
