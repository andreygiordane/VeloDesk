// SPDX-License-Identifier: AGPL-3.0-only
// Teste ponta a ponta do VeloDesk: sobe DUAS instâncias reais do app (A = computador acessado, B = quem controla) com
// perfis isolados em pastas temporárias e as guia pelo DevTools Protocol, exercitando autenticação, permissões, arquivos,
// túneis, gravação, Wake-On-LAN etc. Nada de reiniciar/bloquear/encerrar sessão é executado de verdade.
//
// Requisitos: Windows, Node 22+, `npm install` e `node scripts/build-core.cjs win` feitos em desktop/, e um servidor
// VeloDesk local (`docker compose up -d` na raiz do repositório). Uso, a partir de desktop/:
//   VELODESK_DEFAULT_SERVER=ws://127.0.0.1:21116 node tests/e2e.cjs        (ONLY=T1,T7 filtra os grupos de testes)
// Teste ponta a ponta: duas instâncias reais do VeloDesk (A = computador controlado, B = quem controla)
// guiadas pelo DevTools Protocol. Nada de lock/restart/logoff é executado de verdade.
const { spawn, execSync } = require('child_process');
const net = require('net'), dgram = require('dgram');
const fs = require('fs'), os = require('os'), path = require('path'), crypto = require('crypto');
const ROOT = path.resolve(__dirname, '..');
const ELECTRON = ROOT + '/node_modules/electron/dist/electron.exe';
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const procs = [];
const results = [];
function log(ok, name, extra) { results.push([ok, name]); console.log((ok ? 'PASS ' : 'FAIL ') + name + (extra ? '  [' + extra + ']' : '')); }

function launch(name, port, dbg) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'vd-' + name + '-'));
  const env = { ...process.env, APPDATA: dir, VELODESK_PORT: String(port), VELODESK_PRINT_DRYRUN: path.join(dir, 'print.log'), VELODESK_WOL_PORT: '40009' };
  const p = spawn(ELECTRON, ['.', '--hidden', '--user-data-dir=' + path.join(dir, 'ud'), '--remote-debugging-port=' + dbg], { cwd: ROOT, env, stdio: 'ignore' });
  procs.push(p);
  return { p, dir, port, dbg };
}
async function attach(inst) {
  for (let i = 0; i < 80; i++) {
    try {
      const l = await (await fetch(`http://127.0.0.1:${inst.dbg}/json`)).json();
      const pg = l.find((x) => x.type === 'page' && x.url.includes(':' + inst.port));
      if (pg) {
        const ws = new WebSocket(pg.webSocketDebuggerUrl);
        await new Promise((r, j) => { ws.onopen = r; ws.onerror = j; });
        let id = 0; const pend = {};
        ws.onmessage = (e) => { const m = JSON.parse(e.data); if (m.id && pend[m.id]) { pend[m.id](m); delete pend[m.id]; } };
        const call = (method, params) => new Promise((r) => { const i = ++id; pend[i] = r; ws.send(JSON.stringify({ id: i, method, params })); });
        inst.logs = [];
        ws.addEventListener('message', (e) => { const m = JSON.parse(e.data);
          if (m.method === 'Runtime.exceptionThrown') inst.logs.push('EXC ' + JSON.stringify(m.params.exceptionDetails.exception && m.params.exceptionDetails.exception.description || m.params.exceptionDetails.text).slice(0, 300));
          if (m.method === 'Runtime.consoleAPICalled' && ['error', 'warning'].includes(m.params.type)) inst.logs.push(m.params.type + ' ' + m.params.args.map((a) => a.value || a.description || '').join(' ').slice(0, 300)); });
        await call('Runtime.enable', {});
        inst.ev = async (expr) => {
          const r = await call('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true });
          if (r.result.exceptionDetails) throw new Error((r.result.exceptionDetails.exception || {}).description || JSON.stringify(r.result.exceptionDetails));
          return r.result.result.value;
        };
        return inst;
      }
    } catch {}
    await sleep(500);
  }
  throw new Error('não consegui anexar em ' + inst.port);
}
async function until(inst, expr, ms = 15000, label = expr) {
  const t0 = Date.now();
  while (Date.now() - t0 < ms) { try { if (await inst.ev(expr)) return true; } catch {} await sleep(250); }
  throw new Error('timeout esperando: ' + label);
}
const totp = (b32, step) => {
  const A = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'; let bits = '';
  for (const c of b32) bits += A.indexOf(c).toString(2).padStart(5, '0');
  const key = Buffer.from(bits.match(/.{8}/g).map((b) => parseInt(b, 2)));
  const buf = Buffer.alloc(8); buf.writeBigUInt64BE(BigInt(step));
  const h = crypto.createHmac('sha1', key).update(buf).digest(); const o = h[h.length - 1] & 15;
  return String(((h.readUInt32BE(o) & 0x7fffffff) % 1000000)).padStart(6, '0');
};

let A, B, idA, idB;
// Janela de aceite do host (overlay.html?m=invite), guiada como o usuário faria
async function pageOf(inst, re, ms = 15000) {
  const t0 = Date.now();
  while (Date.now() - t0 < ms) {
    const l = await (await fetch(`http://127.0.0.1:${inst.dbg}/json`)).json();
    const pg = l.find((x) => re.test(x.url)); if (pg) return pg;
    await sleep(250);
  }
  throw new Error('janela não apareceu: ' + re);
}
async function evalIn(pg, expr) {
  const ws = new WebSocket(pg.webSocketDebuggerUrl); await new Promise((r) => (ws.onopen = r));
  const v = await new Promise((r) => { ws.onmessage = (e) => { const m = JSON.parse(e.data); if (m.id === 1) r(m.result.result ? m.result.result.value : undefined); }; ws.send(JSON.stringify({ id: 1, method: 'Runtime.evaluate', params: { expression: expr, awaitPromise: true, returnByValue: true } })); });
  ws.close(); return v;
}
async function inviteWin() {
  const pg = await pageOf(A, /overlay\.html\?m=invite/);
  for (let i = 0; i < 40; i++) { if (await evalIn(pg, `!document.getElementById('inv').hidden&&!!document.getElementById('iname').textContent`)) return pg; await sleep(250); }
  throw new Error('janela de convite sem conteúdo');
}
async function acceptA(profId, toggles = []) {
  const pg = await inviteWin();
  if (profId) await evalIn(pg, `document.getElementById('iprof').value='${profId}'; document.getElementById('iprof').onchange(); 0`);
  for (const k of toggles) await evalIn(pg, `document.querySelector('.pbtn[data-k=${k}]').click(); 0`);
  await evalIn(pg, `document.getElementById('accept').click(); 0`);
}
const hasInvite = async () => (await (await fetch(`http://127.0.0.1:${A.dbg}/json`)).json()).some((x) => /m=invite/.test(x.url));
const sha = (buf) => crypto.createHash('sha256').update(buf).digest('hex');
const mkData = (n) => { const b = Buffer.alloc(n); for (let i = 0; i < n; i++) b[i] = (i * 7 + (i >> 8)) & 255; return b; };
async function connectB(pw, { accept = true, wait = true, expectPrompt = null } = {}) {
  await B.ev(`$('target').value=fmt('${idA}'); $('tpw').value='${pw}'; connect(); 0`);
  if (accept) {
    await acceptA(expectPrompt);
  }
  if (wait) await until(B, `!!(pc&&pc.connectionState==='connected'&&V&&V.perms)`, 25000, 'sessão conectada com perfil');
}
async function endSession() {
  try { await B.ev('hangup(); 0'); } catch {}
  await until(A, '!pc', 8000, 'host sem sessão').catch(() => {});
  await until(B, '!pc', 8000, 'viewer sem sessão').catch(() => {});
  await sleep(400);
}
const toasts = (inst) => inst.ev(`[...document.querySelectorAll('.toast')].map(t=>t.textContent).join(' | ')`);
async function test(name, fn) { if (process.env.ONLY && !process.env.ONLY.split(',').some((k) => name.startsWith(k))) return;
  try { await fn(); } catch (e) { log(false, name, e.message.slice(0, 200));
    try { console.log('   diag A:', await toasts(A), '| pc=', await A.ev('!!pc'), 'deciding=', await A.ev('deciding'), 'fails=', await A.ev('fails.length'), 'scrim=', await A.ev(`$('scrim').classList.contains('show')+':'+$('mt').textContent`));
          console.log('   sinal A:', await A.ev(`__sig.slice(-60).join(' ')`), ' | sinal B:', await B.ev(`__sig.slice(-60).join(' ')`));
          console.log('   diag B:', await toasts(B), '| pc=', await B.ev('pc&&pc.connectionState'), 'V=', await B.ev('!!V')); } catch {}
    try { if (await hasInvite()) await evalIn(await pageOf(A, /m=invite/, 1000), `document.getElementById('reject').click(); 0`); } catch {}
    try { await A.ev(`if($('scrim').classList.contains('show')) $('mno').click(); delete document.visibilityState; 0`); await B.ev(`if($('scrim').classList.contains('show')) $('mno').click(); 0`); } catch {}
    try { await endSession(); } catch {} } }

(async () => {
  await sleep(1500);
  A = await attach(launch('A', 21122, 9331)); B = await attach(launch('B', 21121, 9332));
  await until(A, `!!myId&&agentReady`, 30000, 'A online'); await until(B, `!!myId&&agentReady`, 30000, 'B online');
  idA = await A.ev('myId'); idB = await B.ev('myId');
  console.log('A =', idA, ' B =', idB);
  for (const I of [A, B]) await I.ev(`window.__sig=[]; const o_=WebSocket.prototype.send; WebSocket.prototype.send=function(m){ try{const j=JSON.parse(m); if(j.t==='signal') __sig.push('>'+j.data.kind)}catch{} return o_.call(this,m)}; const on_=onSignal; onSignal=(f,d)=>{__sig.push('<'+d.kind); return on_(f,d)}; 0`);
  // instrumentação do host (A): conta entradas enviadas ao agente e chamadas a /api/action
  await A.ev(`window.__in=0; window.__act=[]; const _s=agent.send.bind(agent); agent.send=x=>{window.__in++;_s(x)}; const _f=window.fetch; window.fetch=(u,o)=>{ if(String(u).includes('/api/action')) window.__act.push(u); return _f(u,o); }; 0`);

  await test('T1 sessão com senha temporária + aceite, perfil Padrão', async () => {
    const pw = await A.ev('password');
    await connectB(pw);
    const perms = await B.ev('JSON.stringify(V.perms)');
    const p = JSON.parse(perms);
    log(p.control && !p.restart && !p.logoff && p.lock, 'T1a perfil Padrão chegou ao controlador', perms);
    log(/Em sessão com/.test(await A.ev(`$('hst').textContent`)), 'T1b host mostra "Em sessão com…"');
    await until(B, `$('rv').videoWidth>0`, 15000, 'vídeo remoto').then(() => log(true, 'T1c vídeo remoto chegou ao controlador'));
    const how = await A.ev('S.how'); log(how === 'temp', 'T1d host autenticou por senha temporária (HMAC, sem senha no sinal)', how);
  });


  await test('T1b chat e avisos de acesso', async () => {
    // faixa flutuante no host
    const targets = async () => (await (await fetch('http://127.0.0.1:9331/json')).json()).filter((x) => /overlay\.html/.test(x.url)).map((x) => x.url);
    await sleep(2500);
    let t = await targets();
    log(t.some((u) => /m=mark/.test(u)), 'T1e marca de sessão ativa apareceu no canto do host', t.join(' '));
    const liveTxt = await A.ev(`$('liveTxt').textContent`); log(/está acessando este computador/.test(liveTxt), 'T1f aviso dentro do app do host', liveTxt);
    // chat: controlador -> host (abre sozinho no host)
    await B.ev(`toggleChat(true); chatSend('Olá, estou acessando para ajudar'); 0`); await sleep(2500);
    t = await targets();
    log(t.some((u) => /m=session/.test(u)) && (await A.ev(`chat.log.length`)) === 1 && (await A.ev(`chat.log[0].t`)) === 'Olá, estou acessando para ajudar', 'T1g mensagem chegou ao host e o chat abriu sozinho');
    // host -> controlador
    await A.ev(`chatSend('Pode ir em frente'); 0`); await sleep(900);
    log((await B.ev(`chat.log.filter(m=>!m.me).map(m=>m.t).join('|')`)) === 'Pode ir em frente', 'T1h resposta do host chegou ao controlador');
    // o painel do chat é a janela flutuante: pega o texto renderizado nela
    const chatPage = (await (await fetch('http://127.0.0.1:9331/json')).json()).find((x) => /m=session/.test(x.url));
    const ow = new WebSocket(chatPage.webSocketDebuggerUrl); await new Promise((r) => (ow.onopen = r));
    const txt = await new Promise((r) => { ow.onmessage = (e) => { const m = JSON.parse(e.data); if (m.id === 1) r(m.result.result.value); }; ow.send(JSON.stringify({ id: 1, method: 'Runtime.evaluate', params: { expression: "document.querySelector('.vdp-log').innerText", returnByValue: true } })); });
    ow.close(); log(/Olá, estou acessando para ajudar/.test(txt) && /Pode ir em frente/.test(txt), 'T1i conversa exibida na janela de chat do host', txt.replace(/\n/g, ' ').slice(0, 70));
    // fim da sessão: aviso "não está mais acessando"
    await B.ev('hangup(); 0'); await until(A, '!pc', 8000); await sleep(1800);
    const ended = await A.ev(`$('liveTxt').textContent`);
    log(/não está mais acessando este computador/.test(ended) && (await A.ev(`$('live').classList.contains('ended')`)), 'T1j host avisa que a pessoa parou de acessar', ended);
    const barPage = (await (await fetch('http://127.0.0.1:9331/json')).json()).find((x) => /m=mark/.test(x.url));
    let barTxt = ''; if (barPage) { const bw = new WebSocket(barPage.webSocketDebuggerUrl); await new Promise((r) => (bw.onopen = r));
      barTxt = await new Promise((r) => { bw.onmessage = (e) => { const m = JSON.parse(e.data); if (m.id === 1) r(m.result.result.value); }; bw.send(JSON.stringify({ id: 1, method: 'Runtime.evaluate', params: { expression: "document.getElementById('txt').innerText", returnByValue: true } })); }); bw.close(); }
    log(/não está mais acessando/.test(barTxt), 'T1k a marca também mostra "parou de acessar"', barTxt);
    log((await targets()).every((u) => !/m=session/.test(u)), 'T1l a janela da sessão fechou ao encerrar');
    await sleep(9500);
    log((await targets()).length === 0, 'T1m a marca some sozinha depois do aviso');
  });

  await test('T2 permissões aplicadas no host: Padrão deixa entrada passar', async () => {
    await connectB(await A.ev('password'));
    const before = await A.ev('window.__in');
    await B.ev(`dc.send(JSON.stringify({k:'wh',dx:0,dy:0})); 0`); await sleep(600);
    const after = await A.ev('window.__in');
    log(after === before + 1, 'T2a controle permitido: entrada chegou ao agente', `${before}->${after}`);
  });

  await test('T3 ações negadas pelo perfil + informações do sistema', async () => {
    await B.ev(`dc.send(JSON.stringify({k:'act',a:'restart'})); dc.send(JSON.stringify({k:'act',a:'logoff'})); 0`); await sleep(900);
    const t = await toasts(B);
    log(/não permite essa ação/.test(t) && (await A.ev('window.__act.length')) === 0, 'T3a reiniciar/encerrar recusados e /api/action nunca chamado', t.slice(0, 80));
    await B.ev(`dc.send(JSON.stringify({k:'act',a:'sysinfo'})); 0`);
    await until(B, `$('scrim').classList.contains('show')&&$('mt').textContent==='Informações do sistema'`, 8000, 'modal de sysinfo');
    const txt = await B.ev(`$('mp').textContent`); await B.ev(`$('mok').click(); 0`);
    log(/Computador:/.test(txt) && /Memória:/.test(txt), 'T3b informações do sistema do host exibidas', txt.split('\n')[0]);
  });

  await test('T4 gravação de sessão (controlador)', async () => {
    const rdir = path.join(B.dir, 'rec').replace(/\\/g, '/');
    await B.ev(`api('/api/rec/dir',{dir:'${rdir}'}).then(r=>setPref('recDir',r.dir))`); await sleep(700);
    await B.ev(`startRec($('rv').srcObject,'sai','${idA}'); 0`); await sleep(6500);
    await B.ev(`stopRec(); 0`); await sleep(1800);
    const l = JSON.parse(await B.ev(`api('/api/rec/list').then(x=>JSON.stringify(x))`));
    log(l.length === 1 && l[0].size > 1000, 'T4a gravação salva na pasta escolhida', l.map((x) => x.name + ' ' + x.size + 'B').join(', '));
  });

  await test('T5 binding do canal: SDP adulterado é rejeitado', async () => {
    await endSession();
    const pw = await A.ev('password');
    // o controlador passa a calcular um fingerprint errado: o MAC da resposta não confere no host
    await B.ev(`window.__hm=hmac; hmac=async(k,m)=>m.includes('|answer|')?'00'.repeat(32):window.__hm(k,m); 0`);
    await B.ev(`$('target').value=fmt('${idA}'); $('tpw').value='${pw}'; connect(); 0`);
    await acceptA();
    let t = '';
    for (let i = 0; i < 40 && !/Falha na verificação de segurança/.test(t); i++) { await sleep(250); t = (await toasts(A)) + ' || ' + (await toasts(B)); }
    log(/Falha na verificação de segurança/.test(t) && !(await A.ev('!!(pc&&pc.connectionState===\'connected\')')), 'T5 oferta/resposta com MAC inválido derruba a conexão', t.slice(0, 90));
    await B.ev(`hmac=window.__hm; 0`); await endSession();
  });

  await test('T6 senha errada', async () => {
    await B.ev(`$('target').value=fmt('${idA}'); $('tpw').value='000000'; connect(); 0`);
    await until(B, `!pc`, 12000, 'rejeição'); const t = await toasts(B);
    log(/Senha incorreta/.test(t), 'T6 senha errada recusada', t.slice(0, 60));
  });

  await test('T7 senha fixa (força mínima, sem supervisão, perfil)', async () => {
    let weak = ''; try { await A.ev(`api('/api/password',{password:'12345678'})`); } catch (e) { weak = e.message; }
    log(/comum/.test(weak), 'T7a senha fraca recusada pelo núcleo', weak.slice(0, 60));
    const ok = await A.ev(`api('/api/password',{password:'vento-azul-42'}).then(r=>r.hasPassword)`);
    log(ok === true, 'T7b senha forte aceita');
    await connectB('vento-azul-42', { accept: false });
    const s = JSON.parse(await A.ev('JSON.stringify({how:S.how,via:S.via,restart:S.perms.restart,logoff:S.perms.logoff})'));
    log(s.how === 'fixed' && s.restart === true && s.logoff === false, 'T7c entrou direto (sem pergunta) com perfil "Acesso não supervisionado"', JSON.stringify(s));
    await endSession();
  });

  await test('T8 lista de controle de acesso (ACL)', async () => {
    await A.ev(`setPref('aclOn',true); setPref('acl','999999999'); 0`); await sleep(400);
    await B.ev(`$('target').value=fmt('${idA}'); $('tpw').value='vento-azul-42'; connect(); 0`);
    await until(B, `!pc`, 12000); let t = await toasts(B);
    log(/não está autorizado/.test(t), 'T8a ID fora da lista é bloqueado', t.slice(0, 60));
    await A.ev(`setPref('acl','${idB.slice(0, 3)}*'); 0`); await sleep(400);
    await connectB('vento-azul-42', { accept: false });
    log(true, 'T8b curinga libera o ID certo'); await endSession();
    await A.ev(`setPref('aclOn',false); 0`);
  });

  await test('T9 modos de acesso interativo', async () => {
    await A.ev(`setPref('accessMode','never'); 0`); await sleep(300);
    const pw = await A.ev('password');
    await B.ev(`$('target').value=fmt('${idA}'); $('tpw').value='${pw}'; connect(); 0`);
    await until(B, `!pc`, 12000); let t = await toasts(B);
    log(/recusada/.test(t), 'T9a modo "nunca": senha temporária recusada', t.slice(0, 60));
    await connectB('vento-azul-42', { accept: false });
    log(true, 'T9b modo "nunca": senha fixa continua entrando'); await endSession();
    await A.ev(`setPref('accessMode','window'); Object.defineProperty(document,'visibilityState',{configurable:true,get:()=>'hidden'}); 0`); await sleep(300);
    await B.ev(`$('target').value=fmt('${idA}'); $('tpw').value='${await A.ev('password')}'; connect(); 0`);
    await until(B, `!pc`, 12000); t = await toasts(B);
    log(/recusada/.test(t), 'T9c modo "janela aberta": recusa quando a janela do app está fechada', t.slice(0, 60));
    await A.ev(`delete document.visibilityState; setPref('accessMode','always'); 0`);
  });

  await test('T10 perfil "Compartilhamento de tela" bloqueia a entrada', async () => {
    const pw = await A.ev('password');
    await B.ev(`$('target').value=fmt('${idA}'); $('tpw').value='${pw}'; connect(); 0`);
    await acceptA('ver');
    await until(B, `!!(pc&&pc.connectionState==='connected'&&V&&V.perms)`, 25000);
    const before = await A.ev('window.__in');
    await B.ev(`dc.send(JSON.stringify({k:'wh',dx:0,dy:0})); dcf.send(JSON.stringify({k:'mm',x:.5,y:.5})); dc.send(JSON.stringify({k:'kd',code:'KeyA'})); 0`); await sleep(800);
    const after = await A.ev('window.__in');
    log(after === before, 'T10a entrada IGNORADA no host no modo somente ver', `${before}->${after}`);
    log((await B.ev(`!$('vperm').hidden`)) === true, 'T10b controlador mostra "Somente visualização"');
    await endSession();
  });

  await test('T11 acesso salvo (token) e revogação', async () => {
    await B.ev(`setPref('rememberAccess',true); 0`); await sleep(400);
    const pw = await A.ev('password');
    await connectB(pw);
    await until(B, `saved.has('${idA}')`, 8000, 'token salvo no cofre'); log(true, 'T11a token guardado no cofre do sistema (safeStorage)');
    await endSession();
    await B.ev(`$('target').value=fmt('${idA}'); $('tpw').value=''; connect(); 0`);
    await acceptA();
    await until(B, `!!(pc&&pc.connectionState==='connected'&&V&&V.perms)`, 25000);
    log((await A.ev('S.how')) === 'token', 'T11b reconectou sem digitar senha (prova HMAC do token)');
    await endSession();
    await A.ev(`api('/api/token',{})`);
    await B.ev(`$('target').value=fmt('${idA}'); $('tpw').value=''; connect(); 0`);
    await until(B, `!pc`, 12000); await sleep(500);
    log((await B.ev(`saved.has('${idA}')`)) === false, 'T11c após "Excluir dados de acesso salvos" o token morre e é apagado do controlador');
  });

  await test('T12 autenticação de dois fatores', async () => {
    const s = JSON.parse(await A.ev(`api('/api/2fa/setup',{}).then(r=>JSON.stringify(r))`));
    const now = Math.floor(Date.now() / 30000);
    const bad = await A.ev(`api('/api/2fa/enable',{code:'000000'}).then(r=>r.ok)`);
    const good = await A.ev(`api('/api/2fa/enable',{code:'${totp(s.secret, now)}'}).then(r=>r.ok)`);
    log(bad === false && good === true, 'T12a ativação exige um código válido do app autenticador');
    await A.ev(`refreshCore()`);
    const pw = await A.ev('password');
    await B.ev(`$('target').value=fmt('${idA}'); $('tpw').value='${pw}'; connect(); 0`);
    await until(B, `$('scrim').classList.contains('show')&&$('mt').textContent==='Verificação em duas etapas'`, 15000, 'pedido de código');
    await B.ev(`$('mi').value='123456'; $('mok').click(); 0`);
    await until(B, `!pc`, 12000); const t = await toasts(B);
    log(/Código de verificação incorreto/.test(t), 'T12b código errado recusado', t.slice(0, 60));
    await sleep(500);
    await B.ev(`$('target').value=fmt('${idA}'); $('tpw').value='${pw}'; connect(); 0`);
    await until(B, `$('scrim').classList.contains('show')&&$('mt').textContent==='Verificação em duas etapas'`, 15000);
    await B.ev(`$('mi').value='${totp(s.secret, now + 1)}'; $('mok').click(); 0`);
    await acceptA();
    await until(B, `!!(pc&&pc.connectionState==='connected'&&V&&V.perms)`, 25000);
    log(true, 'T12c código correto + aceite: sessão aberta'); await endSession();
    // replay: o mesmo código não vale duas vezes
    const replay = await A.ev(`api('/api/2fa/verify',{code:'${totp(s.secret, now + 1)}'}).then(r=>r.ok)`);
    log(replay === false, 'T12d o mesmo código não é aceito duas vezes (anti-replay)');
    // desativar (espera o passo seguinte)
    await sleep(31000);
    const nn = Math.floor(Date.now() / 30000);
    const off = await A.ev(`api('/api/2fa/disable',{code:'${totp(s.secret, nn + 1)}'}).then(r=>r.ok)`);
    log(off === true, 'T12e desativar exige código válido'); await A.ev('refreshCore()');
  });

  await test('T13 encerramento por inatividade', async () => {
    await A.ev(`setPref('autoDisc',true); setPref('autoDiscMin',1); 0`); await sleep(400);
    await connectB('vento-azul-42', { accept: false });
    await A.ev(`S.last=Date.now()-120000; 0`);
    await until(A, `!pc`, 25000, 'host encerra por inatividade');
    log(true, 'T13 sessão encerrada por inatividade'); await A.ev(`setPref('autoDisc',false); 0`);
  });

  await test('T14 proxy', async () => {
    // mexe nos campos da tela como o usuário faria (o evento de mudança é o que refaz a conexão)
    await A.ev(`(()=>{ const set=(k,v)=>{ const e=document.querySelector('[data-pref='+k+']'); e.value=v; e.dispatchEvent(new Event('change')); };
      document.querySelector('[data-pref=proxyMode][value=custom]').click(); set('proxyHost','127.0.0.1'); set('proxyPort','1'); })(); 0`);
    await until(A, `stat.text==='Servidor inacessível'`, 25000, 'sem servidor via proxy morto');
    log(true, 'T14a proxy específico inacessível: o app usa o proxy (não vai direto)');
    await A.ev(`document.querySelector('[data-pref=proxyMode][value=never]').click(); 0`);
    await until(A, `stat.ok`, 25000, 'online sem proxy'); log(true, 'T14b "nunca usar proxy" volta a conectar');
    idA = await A.ev('myId');
  });


  await test('T16 convite: janela de aceite, permissões ajustáveis e rejeição', async () => {
    const pw = await A.ev('password');
    await B.ev(`$('target').value=fmt('${idA}'); $('tpw').value='${pw}'; connect(); 0`);
    const pg = await inviteWin();
    const info = JSON.parse(await evalIn(pg, `JSON.stringify({name:document.getElementById('iname').textContent,id:document.getElementById('iid').textContent,btns:document.querySelectorAll('.pbtn').length,profs:document.getElementById('iprof').options.length,det:document.getElementById('d_how').textContent})`));
    log(info.btns === 12 && info.profs >= 4 && /Senha temporária/.test(info.det) && info.id.replace(/\D/g, '') === idB, 'T16a janela de aceite mostra quem pede, método e 12 permissões', JSON.stringify(info));
    await evalIn(pg, `document.getElementById('reject').click(); 0`);
    await until(B, '!pc', 12000); const t = await toasts(B);
    log(/recusada/.test(t) && !(await hasInvite()), 'T16b "Rejeitar" recusa o pedido e fecha a janela', t.slice(0, 50));
    await sleep(400);
    await connectB(await A.ev('password'), { accept: false, wait: false });
    await acceptA('padrao', ['control', 'clipboard']);      // desliga mouse/teclado e área de transferência neste pedido
    await until(B, `!!(pc&&pc.connectionState==='connected'&&V&&V.perms)`, 25000);
    const p = JSON.parse(await B.ev('JSON.stringify(V.perms)'));
    log(p.control === false && p.clipboard === false && p.lock === true && /ajustado/.test(await A.ev('S.profName')), 'T16c permissões ajustadas na janela chegam ao host e ao controlador', JSON.stringify(p));
    await endSession();
    await connectB(await A.ev('password'), { accept: false, wait: false });
    await (await inviteWin(), evalIn(await pageOf(A, /m=invite/), `document.getElementById('viewonly').click(); 0`));
    await until(B, `!!(pc&&pc.connectionState==='connected'&&V&&V.perms)`, 25000);
    const vo = JSON.parse(await B.ev('JSON.stringify(V.perms)'));
    log(Object.values(vo).every((x) => x === false), 'T16d "Só ver a tela" aceita sem nenhuma permissão', JSON.stringify(vo));
    await endSession();
  });

  await test('T17 arquivos nos dois sentidos (com aceite, integridade e limites)', async () => {
    const dA = path.join(A.dir, 'recebidos').replace(/\\/g, '/'), dB = path.join(B.dir, 'recebidos').replace(/\\/g, '/');
    await A.ev(`api('/api/file/dir',{dir:'${dA}'}).then(r=>setPref('fileDir',r.dir))`); await B.ev(`api('/api/file/dir',{dir:'${dB}'}).then(r=>setPref('fileDir',r.dir))`); await sleep(700);
    await connectB(await A.ev('password'));
    // controlador -> host (o host aprova pelo painel flutuante)
    const data = mkData(3_000_000), want = sha(data);
    await B.ev(`(()=>{ const n=${data.length}, b=new Uint8Array(n); for(let i=0;i<n;i++) b[i]=(i*7+(i>>8))&255; sendFiles([new File([b],'..\\\\..\\\\relatorio final.bin')]); })(); 0`);
    const chatPg = await pageOf(A, /m=session/);
    for (let i = 0; i < 40; i++) { if (await evalIn(chatPg, `!!document.querySelector('.vdp-x button[data-c=file-accept]')`)) break; await sleep(250); }
    await evalIn(chatPg, `document.querySelector('.vdp-x button[data-c=file-accept]').click(); 0`);
    await until(B, `xfers.every(x=>x.state==='done')`, 40000, 'envio concluído'); await until(A, `xfers.every(x=>x.state==='done')`, 20000, 'recebimento concluído');
    const f1 = path.join(dA, 'relatorio final.bin');
    log(fs.existsSync(f1) && sha(fs.readFileSync(f1)) === want, 'T17a arquivo de 3 MB chegou íntegro ao host (nome ../.. neutralizado)', fs.existsSync(f1) ? fs.statSync(f1).size + ' B' : 'ausente');
    log(!fs.existsSync(path.join(dA, '..', 'relatorio final.bin')) && fs.readdirSync(dA).length === 1, 'T17b nada foi gravado fora da pasta de destino');
    // host -> controlador
    const d2 = mkData(700_000);
    await A.ev(`(()=>{ const n=${d2.length}, b=new Uint8Array(n); for(let i=0;i<n;i++) b[i]=(i*7+(i>>8))&255; sendFiles([new File([b],'volta.bin')]); })(); 0`);
    await until(B, `xfers.some(x=>x.dir==='in'&&x.state==='offer')`, 15000, 'oferta no controlador');
    await B.ev(`panelCmd('file-accept', xfers.find(x=>x.dir==='in'&&x.state==='offer').uid); 0`);
    await until(B, `xfers.filter(x=>x.dir==='in').every(x=>x.state==='done')`, 30000, 'recebimento no controlador');
    const f2 = path.join(dB, 'volta.bin');
    log(fs.existsSync(f2) && sha(fs.readFileSync(f2)) === sha(d2), 'T17c arquivo do host chegou íntegro ao controlador');
    // recusa
    await B.ev(`sendFiles([new File([new Uint8Array(1000)],'nao.txt')]); 0`);
    await until(A, `xfers.some(x=>x.name==='nao.txt'&&x.state==='offer')`, 15000);
    await evalIn(chatPg, `document.querySelector('.vdp-x button[data-c=file-reject]').click(); 0`);
    await until(B, `xfers.find(x=>x.name==='nao.txt').state==='rejected'`, 10000);
    log(!fs.existsSync(path.join(dA, 'nao.txt')), 'T17d arquivo recusado não é gravado');
    // limite de tamanho do lado que recebe
    await A.ev(`setPref('fileMaxMB',1); 0`); await sleep(500);
    await B.ev(`sendFiles([new File([new Uint8Array(100)],'pequeno.txt')]); window.__big=1; dcFile.send(JSON.stringify({t:'offer',o:1,id:777,name:'enorme.bin',size:5242880})); 0`);
    await until(A, `true`); await sleep(1500);
    log((await A.ev(`xfers.some(x=>x.name==='enorme.bin')`)) === false, 'T17e oferta acima do limite do host é negada sem criar arquivo');
    await A.ev(`setPref('fileMaxMB',2048); 0`);
    await endSession();
    // perfil sem permissão de arquivos
    await B.ev(`$('target').value=fmt('${idA}'); $('tpw').value='${await A.ev('password')}'; connect(); 0`);
    await acceptA('ver');
    await until(B, `!!(pc&&pc.connectionState==='connected'&&V&&V.perms)`, 25000);
    const before = await A.ev('xfers.length');
    await B.ev(`dcFile.send(JSON.stringify({t:'offer',o:1,id:42,name:'burla.txt',size:5})); 0`); await sleep(1200);
    log((await A.ev('xfers.length')) === before && (await A.ev(`canFiles()`)) === false, 'T17f perfil sem "Transferir arquivos" bloqueia ofertas no host');
    await endSession();
  });

  await test('T18 descoberta na rede local e presença', async () => {
    await B.ev(`setPref('discHide',false); setPref('discScan',true); 0`); await A.ev(`setPref('discHide',false); setPref('discScan',true); 0`); await sleep(500);
    await until(A, `api('/api/discovery').then(r=>r.devices.some(d=>d.id==='${idB}'))`, 20000, 'A encontra B na rede');
    const d = JSON.parse(await A.ev(`api('/api/discovery').then(r=>JSON.stringify(r.devices.find(d=>d.id==='${idB}')))`));
    log(d.sameServer === true && d.name, 'T18a o outro VeloDesk da rede aparece na descoberta (mesmo servidor)', JSON.stringify(d));
    await until(B, `api('/api/discovery').then(r=>r.devices.some(d=>d.id==='${idA}'))`, 15000); log(true, 'T18b e vice-versa');
    await A.ev(`pollFound(); 0`); await sleep(800);
    log((await A.ev(`document.querySelectorAll('#homeFound .sc').length`)) >= 1 && !(await A.ev(`$('foundWrap').hidden`)), 'T18c "Encontrados na rede local" aparece na tela inicial');
    await B.ev(`setPref('discHide',true); 0`);
    await until(A, `api('/api/discovery').then(r=>!r.devices.some(d=>d.id==='${idB}'))`, 25000, 'B some da lista');
    log(true, 'T18d "Excluir este dispositivo da descoberta" esconde o computador');
    await B.ev(`setPref('discHide',false); setPref('discScan',false); 0`); await sleep(600);
    log((await B.ev(`api('/api/discovery').then(r=>r.devices.length)`)) === 0, 'T18e "Pesquisar na rede" desligado não lista ninguém');
    await B.ev(`setPref('discScan',true); 0`);
    // presença (online/offline) pelo servidor
    await B.ev(`addRecent('${idA}'); 0`); await until(B, `presenceOk&&online.has('${idA}')`, 8000, 'presença de A');
    log(true, 'T18f servidor informa que o dispositivo conhecido está online');
    await B.ev(`devices['123456780']={name:'',fav:false,at:Date.now()}; refreshPresence(); 0`); await sleep(2500);
    log((await B.ev(`!online.has('123456780')`)) === true, 'T18g ID inexistente aparece offline');
  });


  const sessionAs = async (profId) => {
    await connectB(await A.ev('password'), { accept: false, wait: false });
    await acceptA(profId);
    await until(B, `!!(pc&&pc.connectionState==='connected'&&V&&V.perms)`, 25000, 'sessão conectada');
  };
  const chatClick = async (sel) => {                      // clica no painel flutuante do host (chat/arquivos)
    const pg = await pageOf(A, /m=session/);
    for (let i = 0; i < 40; i++) { if (await evalIn(pg, `!!document.querySelector('${sel}')`)) break; await sleep(250); }
    await evalIn(pg, `document.querySelector('${sel}').click(); 0`);
  };

  await test('T19 papel de parede como miniatura da máquina', async () => {
    const st = await A.ev(`fetch('/api/wallpaper',{headers:{'X-Agent-Token':agentTok}}).then(r=>r.status)`);
    if (st === 404) { log(true, 'T19 este computador não tem papel de parede legível (teste ignorado)'); return; }
    await sessionAs('padrao');
    await until(B, `!!(devices['${idA}']&&devices['${idA}'].wall)`, 12000, 'papel de parede recebido');
    const th = await B.ev(`devices['${idA}'].thumb`);
    log(th.startsWith('data:image/jpeg;base64,') && th.length > 2000, 'T19a o host enviou o papel de parede e o controlador guardou como miniatura', th.length + ' chars');
    await endSession();
    await B.ev(`renderDevices(); 0`);
    log((await B.ev(`document.querySelectorAll('#homeRecent .tile img').length`)) >= 1 && (await B.ev(`document.querySelector('#homeRecent .tile img').src.startsWith('data:image/jpeg')`)), 'T19b a tela inicial mostra o papel de parede na miniatura');
    log((await B.ev(`devices['${idA}'].wall`)) === true, 'T19c o papel de parede não é trocado por captura de tela');
  });

  await test('T20 gerenciador de arquivos remoto', async () => {
    const base = path.join(A.dir, 'fm'); fs.mkdirSync(path.join(base, 'sub'), { recursive: true });
    fs.writeFileSync(path.join(base, 'a.txt'), 'conteudo A'); fs.writeFileSync(path.join(base, 'sub', 'x.bin'), mkData(200000));
    const dB = path.join(B.dir, 'recebidos2').replace(/\\/g, '/');
    await B.ev(`api('/api/file/dir',{dir:'${dB}'}).then(r=>setPref('fileDir',r.dir))`); await sleep(700);
    const b64 = base.replace(/\\/g, '/');
    // perfil Padrão: sem gerenciador
    await sessionAs('padrao');
    const deny = await B.ev(`fmCall('list',{path:'${b64}'}).then(()=>'ok').catch(e=>e.message)`);
    log(/não permite/.test(deny) && (await B.ev(`document.querySelector('#amenu [data-a=files]').disabled`)) === true, 'T20a perfil sem gerenciador: listagem negada e item do menu desativado', deny);
    await endSession();
    // perfil Acesso integral
    await sessionAs('integral');
    const ls = JSON.parse(await B.ev(`fmCall('list',{path:'${b64}'}).then(d=>JSON.stringify(d.entries.map(e=>e.n+(e.d?'/':''))))`));
    log(ls.join(',') === 'sub/,a.txt', 'T20b lista as pastas primeiro e depois os arquivos', ls.join(','));
    await B.ev(`toggleChat(true,'fm'); 0`); await until(B, `document.querySelectorAll('#chatPanel .vdp-fr').length>0`, 12000, 'aba do gerenciador na tela'); log(true, 'T20c a aba "Gerenciador" mostra as pastas do computador remoto');
    await B.ev(`fmCall('mkdir',{path:'${b64}',name:'nova'}).then(()=>0)`);
    await B.ev(`fmCall('rename',{path:'${b64}/a.txt',name:'b.txt'}).then(()=>0)`);
    log(fs.existsSync(path.join(base, 'nova')) && fs.existsSync(path.join(base, 'b.txt')) && !fs.existsSync(path.join(base, 'a.txt')), 'T20d criar pasta e renomear funcionam no disco do host');
    // download (host -> controlador)
    await B.ev(`fmCmd('fm-get','${b64}/sub/x.bin'); 0`);
    await until(B, `xfers.some(x=>x.name==='x.bin'&&x.state==='done')`, 25000, 'download concluído');
    log(sha(fs.readFileSync(path.join(dB, 'x.bin'))) === sha(mkData(200000)), 'T20e baixar um arquivo do host chega íntegro, sem pedir confirmação ao host');
    // upload para a pasta escolhida (controlador -> host)
    await B.ev(`(()=>{ const n=150000,b=new Uint8Array(n); for(let i=0;i<n;i++) b[i]=(i*7+(i>>8))&255; sendFiles([new File([b],'up.bin')],{dest:'${b64}/nova'}); })(); 0`);
    await until(B, `xfers.some(x=>x.name==='up.bin'&&x.state==='done')`, 25000, 'upload concluído');
    log(fs.existsSync(path.join(base, 'nova', 'up.bin')) && sha(fs.readFileSync(path.join(base, 'nova', 'up.bin'))) === sha(mkData(150000)), 'T20f enviar para uma pasta do host grava lá, íntegro');
    // proteções
    const home = os.homedir().replace(/\\/g, '/');
    const pr = await B.ev(`fmCall('delete',{path:'${home}'}).then(()=>'apagou').catch(e=>e.message)`);
    log(/protegido/.test(pr) && fs.existsSync(os.homedir()), 'T20g a pasta pessoal não pode ser apagada', pr);
    if (process.env.SystemRoot) {
      await B.ev(`sendFiles([new File([new Uint8Array(10)],'x.dll')],{dest:'${process.env.SystemRoot.replace(/\\/g, '/')}/System32'}); 0`);
      await until(B, `xfers.some(x=>x.name==='x.dll'&&x.state==='error')`, 10000, 'upload em pasta do sistema recusado');
      log(!fs.existsSync(path.join(process.env.SystemRoot, 'System32', 'x.dll')), 'T20h upload para pasta do sistema é recusado');
    }
    await B.ev(`fmCall('delete',{path:'${b64}/b.txt'}).then(()=>0)`);
    log(!fs.existsSync(path.join(base, 'b.txt')), 'T20i excluir remove o arquivo no host');
    await endSession();
  });

  await test('T21 túneis TCP', async () => {
    const echo = net.createServer((c) => c.pipe(c)); await new Promise((r) => echo.listen(0, '127.0.0.1', r)); const eport = echo.address().port;
    const rt = (port, data) => new Promise((res, rej) => { const c = net.connect(port, '127.0.0.1'); const chunks = []; c.on('data', (d) => { chunks.push(d); if (Buffer.concat(chunks).length >= data.length) { c.end(); res(Buffer.concat(chunks)); } }); c.on('error', rej); c.on('connect', () => c.write(data)); setTimeout(() => rej(new Error('sem resposta')), 8000); });
    await sessionAs('padrao');
    await B.ev(`$('tunL').value='40123'; $('tunP').value='${eport}'; $('tunAdd').onclick(); 0`); await sleep(800);
    log((await B.ev('tuns.length')) === 0, 'T21a perfil sem túneis não abre túnel');
    await endSession();
    await sessionAs('integral');
    await B.ev(`$('tunL').value='40123'; $('tunH').value='127.0.0.1'; $('tunP').value='${eport}'; $('tunAdd').onclick(); 0`);
    await until(B, 'tuns.length===1', 8000, 'túnel aberto');
    const big = mkData(250000);
    const back = await rt(40123, Buffer.from('ping pelo túnel'));
    log(back.toString() === 'ping pelo túnel', 'T21b dados atravessam o túnel e voltam (porta local → host → destino)');
    const back2 = await rt(40123, big);
    log(sha(back2) === sha(big), 'T21c 250 KB passam íntegros pelo túnel');
    // destino fora da rede local é bloqueado pelo host
    await B.ev(`$('tunL').value='40124'; $('tunH').value='8.8.8.8'; $('tunP').value='53'; $('tunAdd').onclick(); 0`); await until(B, 'tuns.length===2', 8000);
    const blocked = await new Promise((res) => { const c = net.connect(40124, '127.0.0.1'); let got = 0; c.on('data', (d) => (got += d.length)); c.on('close', () => res(got === 0)); c.on('error', () => res(true)); c.on('connect', () => c.write('x')); setTimeout(() => res(false), 9000); });
    log(blocked, 'T21d destino na internet (8.8.8.8) é recusado pelo computador remoto');
    await endSession(); await sleep(600);
    const closed = await new Promise((res) => { const c = net.connect(40123, '127.0.0.1'); c.on('connect', () => { c.destroy(); res(false); }); c.on('error', () => res(true)); });
    log(closed, 'T21e ao encerrar a sessão os túneis são fechados');
    echo.close();
  });

  await test('T22 impressão remota', async () => {
    const plog = path.join(A.dir, 'print.log'); try { fs.unlinkSync(plog); } catch {}
    await sessionAs('padrao');
    await B.ev(`sendFiles([new File([new Uint8Array(300)],'doc.pdf')],{print:'x'}); 0`); await sleep(1200);
    log((await B.ev(`xfers.some(x=>x.name==='doc.pdf'&&x.state==='error')`)) === false || (await A.ev(`xfers.length`)) === 0, 'T22a perfil sem impressão: pedido negado');
    await endSession();
    await sessionAs('integral');
    const pl = JSON.parse(await B.ev(`fmCall('printers').then(d=>JSON.stringify(d.printers))`));
    log(pl.includes('Impressora de teste'), 'T22b o controlador lista as impressoras do computador remoto', pl.join(','));
    await B.ev(`sendFiles([new File([new Uint8Array(300)],'doc.pdf')],{print:'Impressora de teste'}); 0`);
    await chatClick('.vdp-x button[data-c=file-accept]');
    await until(B, `xfers.some(x=>x.name==='doc.pdf'&&x.state==='done')`, 20000, 'envio concluído');
    for (let i = 0; i < 30 && !fs.existsSync(plog); i++) await sleep(250);
    const logTxt = fs.existsSync(plog) ? fs.readFileSync(plog, 'utf8') : '';
    log(/^Impressora de teste\|.*doc\.pdf/m.test(logTxt), 'T22c após o aceite do host, o documento vai para a impressora escolhida', logTxt.trim());
    await B.ev(`sendFiles([new File([new Uint8Array(20)],'perigo.exe')],{print:''}); 0`);
    await until(B, `xfers.some(x=>x.name==='perigo.exe'&&x.state==='error')`, 10000, 'tipo recusado');
    log(!/perigo/.test(fs.existsSync(plog) ? fs.readFileSync(plog, 'utf8') : ''), 'T22d executável não pode ser impresso (tipo bloqueado)');
    await endSession();
  });

  await test('T23 Wake-On-LAN', async () => {
    const got = []; const udp = dgram.createSocket({ type: 'udp4', reuseAddr: true });
    await new Promise((r) => udp.bind(40009, r)); udp.on('message', (m) => got.push(m));
    const waitPkt = async (n, ms = 4000) => { const t0 = Date.now(); while (got.length < n && Date.now() - t0 < ms) await sleep(100); return got.length >= n; };
    const macOf = (b) => [...b.subarray(6, 12)].map((x) => x.toString(16).padStart(2, '0')).join(':');
    // envio local
    await B.ev(`api('/api/wol/send',{mac:'aa:bb:cc:dd:ee:01'}).then(()=>0)`);
    const ok1 = await waitPkt(1);
    log(ok1 && got[0].length === 102 && macOf(got[0]) === 'aa:bb:cc:dd:ee:01' && got[0].subarray(0, 6).every((x) => x === 255) && got[0].subarray(96, 102).equals(got[0].subarray(6, 12)), 'T23a pacote mágico (6×FF + 16×MAC) enviado por broadcast', got[0] ? got[0].length + ' B ' + macOf(got[0]) : 'nada');
    const bad = await B.ev(`api('/api/wol/send',{mac:'nao-e-mac'}).then(()=>'aceito').catch(e=>e.message)`);
    log(bad !== 'aceito', 'T23b MAC inválido é recusado', bad);
    await sleep(2200);
    // relay: o pedido passa por outro computador (A) da mesma rede
    const n0 = got.length;
    await A.ev(`setPref('wolMode','list'); setPref('wolList','999999999'); 0`); await sleep(500);
    await B.ev(`send('${idA}',{kind:'wake',mac:'aa:bb:cc:dd:ee:02'}); 0`); await sleep(2500);
    log(got.length === n0, 'T23c modo "só estes IDs": pedido de ID fora da lista é ignorado');
    await A.ev(`setPref('wolList','${idB}'); 0`); await sleep(500);
    await B.ev(`send('${idA}',{kind:'wake',mac:'aa:bb:cc:dd:ee:02'}); 0`);
    log((await waitPkt(n0 + 1, 5000)) && macOf(got[n0]) === 'aa:bb:cc:dd:ee:02', 'T23d ID autorizado: o outro computador envia o pacote para a rede dele');
    await A.ev(`setPref('wolMode','off'); 0`); await sleep(500); const n1 = got.length;
    await B.ev(`send('${idA}',{kind:'wake',mac:'aa:bb:cc:dd:ee:03'}); 0`); await sleep(2500);
    log(got.length === n1, 'T23e modo "desabilitado": pedidos são ignorados');
    // o MAC é anunciado na descoberta (e deixa de ser com o recurso desligado)
    await A.ev(`setPref('wolMode','on'); 0`); await B.ev(`setPref('wolMode','on'); 0`); await sleep(500);
    await until(A, `api('/api/discovery').then(r=>r.devices.some(d=>d.id==='${idB}'&&/^([0-9a-f]{2}:){5}[0-9a-f]{2}$/i.test(d.mac||'')))`, 20000, 'MAC anunciado');
    log(true, 'T23f o computador anuncia o MAC na rede (para quem quiser acordá-lo)');
    await B.ev(`setPref('wolMode','off'); 0`);
    await until(A, `api('/api/discovery').then(r=>r.devices.some(d=>d.id==='${idB}'&&!d.mac))`, 20000, 'MAC deixa de ser anunciado');
    log(true, 'T23g com o recurso desligado o MAC não é anunciado');
    await B.ev(`setPref('wolMode','on'); 0`);
    udp.close();
  });


  await test('T25 marca discreta, janela da sessão e permissões ao vivo', async () => {
    await sessionAs('integral'); await sleep(2500);
    const urls = (await (await fetch(`http://127.0.0.1:${A.dbg}/json`)).json()).map((x) => x.url);
    log(urls.some((u) => /m=mark/.test(u)) && urls.some((u) => /m=session/.test(u)) && !urls.some((u) => /m=bar/.test(u)), 'T25a marca no canto + janela da sessão, sem barra no topo');
    const mt = await evalIn(await pageOf(A, /m=mark/), `document.getElementById('txt').innerText`);
    log(/Sessão remota ativa/.test(mt), 'T25b texto da marca', mt);
    const ses = await pageOf(A, /m=session/);
    const info = JSON.parse(await evalIn(ses, `JSON.stringify({name:document.getElementById('sname').textContent,time:document.getElementById('stime').textContent,btns:document.querySelectorAll('#spgrid .pbtn').length,prof:document.getElementById('sprof').value,disc:!!document.getElementById('sdisc')})`));
    log(info.btns === 12 && /Conectado/.test(info.time) && info.disc && info.prof === 'integral', 'T25c janela da sessão mostra perfil, 12 permissões, tempo e Desconectar', JSON.stringify(info));
    // desligar mouse/teclado ao vivo
    const before = await A.ev('window.__in');
    await evalIn(ses, `document.querySelector('#spgrid .pbtn[data-k=control]').click(); 0`);
    await until(B, `V.perms.control===false`, 8000, 'controlador recebe a mudança');
    await B.ev(`dc.send(JSON.stringify({k:'wh',dx:0,dy:0})); 0`); await sleep(700);
    log((await A.ev('window.__in')) === before && (await B.ev(`!$('vperm').hidden`)) === true, 'T25d desligar "mouse e teclado" na janela bloqueia a entrada na hora e avisa o controlador');
    // trocar de perfil
    await evalIn(ses, `document.getElementById('sprof').value='ver'; document.getElementById('sprof').onchange(); 0`);
    await until(B, `Object.values(V.perms).every(x=>x===false)`, 8000, 'perfil somente ver');
    log(true, 'T25e trocar o perfil na janela vale na hora');
    // desconectar
    await evalIn(ses, `document.getElementById('sdisc').click(); 0`);
    await until(A, '!pc', 8000, 'host sem sessão'); await until(B, '!pc', 8000, 'controlador sem sessão');
    log(true, 'T25f "Desconectar" encerra a sessão dos dois lados');
  });

  await test('T26 cursor só da máquina remota e afinação de latência', async () => {
    await sessionAs('padrao'); await sleep(3500);
    const i = JSON.parse(await B.ev(`JSON.stringify({cursor:getComputedStyle($('rv')).cursor, jb:pc.getReceivers().filter(r=>r.track&&r.track.kind==='video').map(r=>r.jitterBufferTarget), fps:P.fps, q:P.quality})`));
    log(i.cursor === 'none', 'T26a o cursor local fica escondido (some a "mira"); só aparece o cursor da máquina remota', i.cursor);
    log(i.jb.length === 1 && i.jb[0] === 0, 'T26b recebimento sem buffer extra (jitterBufferTarget = 0)', JSON.stringify(i.jb));
    log(i.fps === 60 && i.q === 'balanced', 'T26c padrão: 60 quadros e prioridade equilibrada (não sacrifica a resolução)', i.fps + ' fps / ' + i.q);
    const st = JSON.parse(await B.ev(`(async()=>{ const s=await pc.getStats(); let o={}; s.forEach(r=>{ if(r.type==='inbound-rtp'&&r.kind==='video'){ o={fps:r.framesPerSecond, jbMs:r.jitterBufferEmittedCount?Math.round(r.jitterBufferDelay/r.jitterBufferEmittedCount*1000):null, w:r.frameWidth, h:r.frameHeight}; } }); return JSON.stringify(o); })()`));
    console.log('   (informativo) vídeo recebido: ' + JSON.stringify(st));
    await B.ev(`setPref('showLocalCursor',true); $('rv').style.cursor='default'; 0`);
    log((await B.ev(`getComputedStyle($('rv')).cursor`)) === 'default', 'T26d opção "mostrar também o cursor deste computador" funciona');
    await B.ev(`setPref('showLocalCursor',false); 0`);
    await endSession();
  });


  await test('T27 aviso de versão antiga no outro computador', async () => {
    await sessionAs('padrao'); await sleep(1500);
    log((await B.ev(`$('vver').hidden`)) === true, 'T27a host na mesma versão: nenhum aviso');
    // simula um host antigo: o "hello" chega sem a versão
    await B.ev(`viewerMsg(JSON.stringify({k:'hello',perms:V.perms})); 0`); await sleep(500);
    const t = await B.ev(`$('vver').textContent`);
    log((await B.ev(`$('vver').hidden`)) === false && /antiga/.test(t), 'T27b host sem informar versão (antigo): aparece o aviso para o controlador', t);
    await B.ev(`viewerMsg(JSON.stringify({k:'hello',perms:V.perms,ver:'0.0.9'})); 0`); await sleep(300);
    log(/0\.0\.9/.test(await B.ev(`$('vver').textContent`)), 'T27c host com versão menor informada: o aviso mostra qual é');
    await endSession();
  });


  await test('T28 qualidade do vídeo logo no início da sessão', async () => {
    await connectB(await A.ev('password'), { accept: false, wait: false });
    await acceptA('padrao');
    await until(B, `!!(pc&&pc.connectionState==='connected')`, 25000, 'conectado');
    const target = await A.ev(`stream.getVideoTracks()[0].getSettings().width`);
    const series = []; const t0 = Date.now();
    for (let i = 0; i < 20; i++) {
      const o = JSON.parse(await B.ev(`(async()=>{ const s=await pc.getStats(); let o={}; s.forEach(r=>{ if(r.type==='inbound-rtp'&&r.kind==='video') o={w:r.frameWidth||0,fps:Math.round(r.framesPerSecond||0),kbps:0}; }); return JSON.stringify(o); })()`));
      series.push({ t: ((Date.now() - t0) / 1000).toFixed(1), w: o.w, fps: o.fps }); await sleep(400);
    }
    console.log('   largura recebida ao longo do tempo (s:px@fps): ' + series.map((x) => x.t + ':' + x.w + '@' + x.fps).join('  '));
    const first = series.find((x) => x.w > 0);
    const at2 = series.find((x) => Number(x.t) >= 2.0);
    log(!!first && first.w >= target * 0.9, 'T28a o primeiro quadro já chega em resolução cheia (' + target + ' px de largura)', first ? first.w + ' px' : 'sem quadros');
    log(!!at2 && at2.w >= target * 0.9, 'T28b aos 2 segundos a resolução continua cheia', at2 ? at2.w + ' px' : '?');
    await endSession();
  });

  await test('T29 janela da sessão compacta, sem cobrir a tela de quem controla', async () => {
    await sessionAs('padrao'); await sleep(2500);
    const ses = await pageOf(A, /m=session/);
    const geo = async () => JSON.parse(await evalIn(ses, `JSON.stringify({w:outerWidth,h:outerHeight,y:screenY,top:screenY,ah:screen.availHeight,compact:document.body.classList.contains('compact'),
      grid:document.querySelector('#spgrid').offsetParent!==null,disc:document.querySelector('#sdisc').offsetParent!==null,
      avText:document.getElementById('sav').textContent.trim(),avSvg:!!document.querySelector('#sav svg use[href="#bolt"]')})`));
    let g = await geo();
    log(g.compact && g.w <= 420 && g.h <= 260 && g.y > g.ah / 2, 'T29a a janela da sessão abre compacta e na parte de baixo da tela (não cobre a barra do topo)', JSON.stringify({ w: g.w, h: g.h, y: g.y, alturaUtil: g.ah }));
    log(!g.grid && g.disc, 'T29b no modo compacto só aparecem o nome, o tempo e o botão Desconectar');
    log(g.avText === '' && g.avSvg, 'T29c o avatar mostra o raio do VeloDesk e não a primeira letra/número', JSON.stringify({ texto: g.avText, raio: g.avSvg }));
    await evalIn(ses, `document.getElementById('sexp').click(); 0`); await sleep(900);
    g = await geo();
    log(!g.compact && g.w >= 840 && g.grid, 'T29d o botão expandir mostra permissões, bate-papo e arquivos', g.w + 'x' + g.h);
    await evalIn(ses, `document.getElementById('sexp').click(); 0`); await sleep(900);
    g = await geo();
    log(g.compact && g.w <= 420, 'T29e recolher volta para o canto');
    await B.ev(`toggleChat(true); chatSend('Preciso da sua ajuda aqui'); 0`); await sleep(2500);
    g = await geo();
    log(!g.compact && g.w >= 840, 'T29f mensagem nova do controlador expande a janela sozinha para o host ver');
    await evalIn(ses, `document.getElementById('sdisc').click(); 0`);
    await until(A, '!pc', 8000, 'host sem sessão'); await until(B, '!pc', 8000, 'controlador sem sessão');
  });

  await test('T15 canal de atualização', async () => {
    await A.ev(`setPref('updateMode','off'); 0`); await sleep(600);
    await A.ev(`fetch('/api/update?do=check',{method:'POST'}); 0`); await sleep(1500);
    const st = await A.ev(`api('/api/update').then(r=>r.state)`);
    log(st === 'off', 'T15 atualizações "desativadas" respeitadas pelo núcleo', st);
  });

  console.log('\n' + results.filter((r) => r[0]).length + ' de ' + results.length + ' verificações passaram');
  for (const p of procs) { try { execSync('taskkill /F /PID ' + p.pid + ' /T', { stdio: 'ignore' }); } catch {} }
  process.exit(results.every((r) => r[0]) ? 0 : 1);
})().catch((e) => { console.error('ERRO FATAL', e); for (const p of procs) { try { execSync('taskkill /F /PID ' + p.pid + ' /T', { stdio: 'ignore' }); } catch {} } process.exit(2); });
