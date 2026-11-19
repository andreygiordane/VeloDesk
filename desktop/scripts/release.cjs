// SPDX-License-Identifier: AGPL-3.0-only
// Publica uma atualização assinada para os clientes.
//   npm run release -- <versão> "<notas>"
// 1) compila o núcleo (Linux e Windows) com a versão  2) assina com sua chave  3) grava no diretório
// que o servidor distribui (velodesk server -updates <dir>). O servidor avisa os clientes sozinho.
const { execFileSync } = require('child_process');
const path = require('path');
const fs = require('fs');
const os = require('os');

const [version, notes = ''] = process.argv.slice(2);
if (!/^\d+\.\d+\.\d+$/.test(version || '')) { console.error('uso: npm run release -- 0.3.1 "o que mudou"'); process.exit(1); }
const key = process.env.VELODESK_UPDATE_KEY || path.join(os.homedir(), '.config', 'velodesk-publish', 'update.key');
const out = process.env.VELODESK_UPDATES_DIR || path.join(os.homedir(), 'velodesk-updates');
if (!fs.existsSync(key)) { console.error('chave de assinatura não encontrada em ' + key + ' (rode: velodesk keygen)'); process.exit(1); }

const core = path.join(__dirname, '..', '..', 'core');
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'velodesk-rel-'));
const build = (goos, name) => {
  execFileSync('go', ['build', '-trimpath', '-ldflags', `-s -w -X main.version=${version}`, '-o', path.join(tmp, name), './cmd/velodesk'],
    { cwd: core, stdio: 'inherit', env: { ...process.env, CGO_ENABLED: '0', GOOS: goos, GOARCH: 'amd64' } });
};
build('linux', 'velodesk-linux'); build('windows', 'velodesk-windows.exe');
execFileSync('go', ['run', './cmd/velodesk', 'publish', '-key', key, '-version', version, '-notes', notes, '-out', out,
  '-linux-amd64', path.join(tmp, 'velodesk-linux'), '-windows-amd64', path.join(tmp, 'velodesk-windows.exe')],
  { cwd: core, stdio: 'inherit' });
fs.rmSync(tmp, { recursive: true, force: true });
console.log(`\nPublicado em ${out}. Servidor: velodesk server -updates ${out}`);
