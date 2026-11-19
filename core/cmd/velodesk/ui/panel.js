// SPDX-License-Identifier: AGPL-3.0-only
/* Painel "Bate-papo | Arquivos", usado pela janela principal e pela janela flutuante do computador controlado.
   VDPanel(raiz, {state(), cmd(nome, argumento)}) -> {render(), tab(nome)}
   state(): {name, log:[{me,t,at}], unread, files:[...], canFiles, tab}
   Cada arquivo: {uid, dir:'in'|'out', name, size, done, state:'offer'|'wait'|'active'|'done'|'rejected'|'canceled'|'error', err} */
(function () {
  const css = `
.vdp{display:flex;flex-direction:column;height:100%;min-height:0;color:var(--ink);font:13px/1.4 "Segoe UI",system-ui,sans-serif}
.vdp *{box-sizing:border-box}
.vdp button{font:inherit;cursor:pointer;border:0;color:inherit}
.vdp-tabs{display:flex;border-bottom:1px solid var(--line)}
.vdp-tabs button{flex:1;position:relative;padding:11px 8px;background:transparent;color:var(--muted);font-weight:600;border-bottom:2px solid transparent}
.vdp-tabs button.on{color:var(--ink);border-bottom-color:var(--accent)}
.vdp-tabs i{position:absolute;top:8px;right:22%;width:8px;height:8px;border-radius:50%;background:var(--bad)}
.vdp-tabs i[hidden]{display:none}
.vdp section{flex:1;min-height:0;display:flex;flex-direction:column}
.vdp section[hidden]{display:none}
.vdp-log{flex:1;overflow-y:auto;padding:12px;display:flex;flex-direction:column;gap:8px;user-select:text}
.vdp-none{margin:auto;color:var(--muted);text-align:center}
.vdp-msg{max-width:84%;padding:8px 11px;border-radius:12px;background:var(--soft,var(--card2));align-self:flex-start;word-break:break-word;white-space:pre-wrap}
.vdp-msg.me{align-self:flex-end;background:var(--accent);color:#fff}
.vdp-msg small{display:block;opacity:.65;font-size:11px;margin-top:2px}
.vdp-form{display:flex;gap:8px;padding:10px;border-top:1px solid var(--line)}
.vdp-form input{flex:1;min-width:0;background:var(--soft,var(--card2));border:1px solid var(--line);border-radius:9px;padding:9px 11px;color:var(--ink);font:inherit;outline:none;user-select:text}
.vdp-form input:focus{border-color:var(--accent)}
.vdp-form button{background:var(--accent);color:#fff;border-radius:9px;padding:0 14px;font-weight:600}
.vdp-drop{margin:12px;padding:16px;border:1.5px dashed var(--line);border-radius:12px;text-align:center;color:var(--muted)}
.vdp-drop.over{border-color:var(--accent);background:rgba(139,61,255,.1)}
.vdp-drop button{background:var(--accent);color:#fff;border-radius:9px;padding:7px 14px;font-weight:600;margin-top:8px}
.vdp-off{margin:auto;padding:20px;text-align:center;color:var(--muted)}
.vdp-list{flex:1;overflow-y:auto;padding:0 12px 12px;display:flex;flex-direction:column;gap:8px}
.vdp-x{background:var(--soft,var(--card2));border:1px solid var(--line);border-radius:12px;padding:10px 12px}
.vdp-x b{display:block;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.vdp-x .meta{color:var(--muted);font-size:12px;margin-top:2px}
.vdp-x .bar{height:5px;border-radius:4px;background:var(--line);margin-top:8px;overflow:hidden}
.vdp-x .bar span{display:block;height:100%;background:var(--accent)}
.vdp-x .warn{margin-top:6px;color:#f5a524;font-size:12px}
.vdp-x .row{display:flex;gap:6px;margin-top:8px;flex-wrap:wrap}
.vdp-x .row button{padding:5px 11px;border-radius:8px;background:var(--card);border:1px solid var(--line);font-weight:600}
.vdp-x .row button.ok{background:var(--ok,#2dd36f);color:#06210f;border-color:transparent}
.vdp-x .row button.no{background:var(--bad);color:#fff;border-color:transparent}
.vdp-x.err .meta{color:var(--bad)}
.vdp-fmbar{display:flex;align-items:center;gap:6px;padding:8px 10px;border-bottom:1px solid var(--line)}
.vdp-fmbar button,.vdp-fmact button{padding:5px 10px;border-radius:8px;background:var(--card);border:1px solid var(--line);font-weight:600}
.vdp-path{flex:1;min-width:0;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;color:var(--muted);direction:rtl;text-align:left}
.vdp-fmact{display:flex;gap:6px;padding:8px 10px;flex-wrap:wrap}
.vdp-fmlist{flex:1;overflow-y:auto;padding:0 6px 8px}
.vdp-fr{display:flex;align-items:center;gap:8px;padding:6px 8px;border-radius:8px;cursor:default}
.vdp-fr:hover{background:var(--soft,var(--card2))}
.vdp-fr[data-d="1"]{cursor:pointer}
.vdp-fr .nm{flex:1;min-width:0;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.vdp-fr .sz,.vdp-fr .dt{color:var(--muted);font-size:12px;white-space:nowrap}
.vdp-fr .ac{display:none;gap:4px}
.vdp-fr:hover .ac{display:flex}
.vdp-fr .ac button{padding:3px 8px;border-radius:7px;background:var(--card);border:1px solid var(--line);font-size:12px}`;
  const st = document.createElement('style'); st.textContent = css; document.head.append(st);

  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const size = (n) => n < 1024 ? n + ' B' : n < 1048576 ? (n / 1024).toFixed(0) + ' KB' : n < 1073741824 ? (n / 1048576).toFixed(1) + ' MB' : (n / 1073741824).toFixed(2) + ' GB';
  const RISKY = /\.(exe|msi|bat|cmd|ps1|vbs|vbe|js|jse|jar|scr|com|lnk|dll|sh|apk|reg|hta)$/i;
  const LABEL = { wait: 'Aguardando o outro lado aceitar…', rejected: 'Recusado', canceled: 'Cancelado', done: 'Concluído' };

  window.VDPanel = function (root, opt) {
    root.classList.add('vdp');
    root.innerHTML = `<div class="vdp-tabs"><button data-t="chat" class="on">Bate-papo<i hidden></i></button><button data-t="files">Arquivos<i hidden></i></button><button data-t="fm" hidden>Gerenciador</button></div>
<section data-p="chat"><div class="vdp-log"></div><form class="vdp-form"><input placeholder="Mensagem" maxlength="1000" autocomplete="off"><button>Enviar</button></form></section>
<section data-p="files" hidden><div class="vdp-drop"><div>Arraste arquivos para cá</div><button type="button" class="pick">Enviar arquivo…</button><input type="file" multiple hidden></div><div class="vdp-off" hidden></div><div class="vdp-list"></div></section>
<section data-p="fm" hidden><div class="vdp-fmbar"><button data-c="fm-up" title="Pasta acima">↑</button><span class="vdp-path"></span><button data-c="fm-refresh" title="Atualizar">⟳</button></div><div class="vdp-fmact"><button data-c="fm-mkdir">Nova pasta</button><button data-c="fm-pick">Enviar para esta pasta…</button><input type="file" multiple hidden class="fmfile"></div><div class="vdp-fmlist"></div></section>`;
    const $ = (s) => root.querySelector(s);
    const input = $('.vdp-form input'), file = $('input[type=file]'), drop = $('.vdp-drop');
    let tab = 'chat', lastHint = '';
    const show = (t) => {
      tab = t;
      root.querySelectorAll('[data-t]').forEach((b) => b.classList.toggle('on', b.dataset.t === t));
      root.querySelectorAll('[data-p]').forEach((p) => (p.hidden = p.dataset.p !== t));
      if (t === 'chat') setTimeout(() => input.focus(), 30);
    };
    root.querySelectorAll('[data-t]').forEach((b) => (b.onclick = () => { show(b.dataset.t); render(); }));
    const fmfile = root.querySelector('.fmfile');
    root.querySelector('.vdp-fmbar').onclick = (e) => { const b = e.target.closest('button[data-c]'); if (b) opt.cmd(b.dataset.c); };
    root.querySelector('.vdp-fmact').onclick = (e) => { const b = e.target.closest('button[data-c]'); if (!b) return; if (b.dataset.c === 'fm-pick') fmfile.click(); else opt.cmd(b.dataset.c); };
    fmfile.onchange = () => { if (fmfile.files.length) opt.cmd('fm-upload', Array.from(fmfile.files)); fmfile.value = ''; };
    root.querySelector('.vdp-fmlist').onclick = (e) => {
      const row = e.target.closest('.vdp-fr'); if (!row) return;
      const b = e.target.closest('button[data-c]');
      if (b) return opt.cmd(b.dataset.c, row.dataset.p);
      if (row.dataset.d === '1') opt.cmd('fm-open', row.dataset.p);
    };
    $('.vdp-form').onsubmit = (e) => { e.preventDefault(); const t = input.value.trim(); if (t) opt.cmd('chat-send', t); input.value = ''; };
    $('.pick').onclick = () => file.click();
    file.onchange = () => { if (file.files.length) opt.cmd('file-send', Array.from(file.files)); file.value = ''; };
    ['dragenter', 'dragover'].forEach((ev) => root.addEventListener(ev, (e) => { e.preventDefault(); if (tab === 'files') drop.classList.add('over'); }));
    ['dragleave', 'drop'].forEach((ev) => root.addEventListener(ev, (e) => { e.preventDefault(); drop.classList.remove('over'); }));
    root.addEventListener('drop', (e) => { if (e.dataTransfer && e.dataTransfer.files.length) { show('files'); opt.cmd('file-send', Array.from(e.dataTransfer.files)); } });

    function item(x) {
      const pct = x.size ? Math.min(100, Math.round((x.done / x.size) * 100)) : 100;
      const dirTxt = x.dir === 'in' ? 'Recebendo' : 'Enviando';
      let meta = x.state === 'active' ? `${dirTxt} · ${size(x.done)} de ${size(x.size)} (${pct}%)` : x.state === 'offer' ? `Quer enviar este arquivo · ${size(x.size)}` : x.state === 'error' ? (x.err || 'Falhou') : `${LABEL[x.state] || ''} · ${size(x.size)}`;
      const btn = (cls, c, t) => `<button class="${cls}" data-c="${c}" data-u="${x.uid}">${t}</button>`;
      let row = '';
      if (x.state === 'offer') row = btn('ok', 'file-accept', 'Aceitar') + btn('no', 'file-reject', 'Recusar');
      else if (x.state === 'active' || x.state === 'wait') row = btn('', 'file-cancel', 'Cancelar');
      else if (x.state === 'done' && x.dir === 'in') row = btn('', 'file-reveal', 'Mostrar na pasta');
      const warn = x.dir === 'in' && x.state === 'offer' && RISKY.test(x.name) ? '<div class="warn">⚠ Este arquivo pode executar programas. Só aceite se você confia em quem enviou.</div>' : '';
      return `<div class="vdp-x ${x.state === 'error' ? 'err' : ''}"><b title="${esc(x.name)}">${x.dir === 'in' ? '⬇' : '⬆'} ${esc(x.name)}</b><div class="meta">${esc(meta)}</div>${x.state === 'active' ? `<div class="bar"><span style="width:${pct}%"></span></div>` : ''}${warn}<div class="row">${row}</div></div>`;
    }
    $('.vdp-list').onclick = (e) => { const b = e.target.closest('button[data-c]'); if (b) opt.cmd(b.dataset.c, Number(b.dataset.u)); };

    function render() {
      const s = opt.state();
      if (s.tab && s.tab !== lastHint) { lastHint = s.tab; show(s.tab); } else if (!s.tab) lastHint = '';
      const log = s.log || [];
      const box = $('.vdp-log'), atEnd = box.scrollTop + box.clientHeight >= box.scrollHeight - 30;
      box.innerHTML = log.length ? log.map((m) => `<div class="vdp-msg ${m.me ? 'me' : ''}">${esc(m.t)}<small>${new Date(m.at).toLocaleTimeString('pt-BR', { hour: '2-digit', minute: '2-digit' })}</small></div>`).join('') : '<div class="vdp-none">Nenhuma mensagem ainda.<br>Escreva abaixo para conversar.</div>';
      if (atEnd) box.scrollTop = box.scrollHeight;
      const files = s.files || [];
      $('.vdp-off').hidden = s.canFiles; $('.vdp-off').textContent = 'O perfil desta sessão não permite transferir arquivos.';
      drop.hidden = !s.canFiles; $('.vdp-list').hidden = !s.canFiles;
      $('.vdp-list').innerHTML = files.length ? files.slice().reverse().map(item).join('') : '<div class="vdp-none" style="padding:20px">Nenhum arquivo ainda.</div>';
      const f = s.fm, fb = root.querySelector('[data-t=fm]'); fb.hidden = !f;
      if (f) {
        if (tab === 'fm' && !f.loaded && !f.busy) opt.cmd('fm-open', '');
        root.querySelector('.vdp-path').textContent = f.path ? '\u200E' + f.path : 'Este computador';
        root.querySelector('[data-c=fm-up]').disabled = !f.path;
        root.querySelector('[data-c=fm-mkdir]').hidden = root.querySelector('[data-c=fm-pick]').hidden = !(f.canWrite && f.path);
        const when = (t) => new Date(t * 1000).toLocaleDateString('pt-BR');
        root.querySelector('.vdp-fmlist').innerHTML = f.busy ? '<div class="vdp-none" style="padding:20px">Carregando…</div>' : f.err ? `<div class="vdp-x err"><div class="meta">${esc(f.err)}</div></div>` : (f.entries || []).map((e) => `<div class="vdp-fr" data-p="${esc(e.p)}" data-d="${e.d ? 1 : 0}"><span>${e.d ? '📁' : '📄'}</span><span class="nm" title="${esc(e.n)}">${esc(e.n)}</span><span class="sz">${e.d ? '' : size(e.s)}</span><span class="dt">${e.m ? when(e.m) : ''}</span><span class="ac">${e.d ? '' : '<button data-c="fm-get">Baixar</button>'}${f.canWrite && f.path ? '<button data-c="fm-rename">Renomear</button><button data-c="fm-delete">Excluir</button>' : ''}</span></div>`).join('') || '<div class="vdp-none" style="padding:20px">Pasta vazia.</div>';
      } else if (tab === 'fm') show('chat');
      root.querySelector('[data-t=chat] i').hidden = !(s.unread && tab !== 'chat');
      root.querySelector('[data-t=files] i').hidden = !files.some((x) => x.state === 'offer') || tab === 'files';
      if (tab === 'chat' && s.unread) opt.cmd('chat-read');
    }
    return { render, tab: show };
  };
})();
