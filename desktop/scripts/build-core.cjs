// SPDX-License-Identifier: AGPL-3.0-only
// Compila o núcleo Go para desktop/resources/<alvo>/.
// Uso: node scripts/build-core.cjs [win] [linux] [mac] [macx64]   (sem argumentos: só o sistema atual)
//   mac = Apple Silicon (arm64) · macx64 = Mac Intel (amd64)
// Servidor padrão embutido (opcional): VELODESK_DEFAULT_SERVER=wss://remoto.seudominio.com
// Servidor fixo e escondido (o usuário não vê nem troca): além do acima, VELODESK_LOCK_SERVER=1
const { execFileSync } = require('child_process');
const path = require('path');
const fs = require('fs');

const here = process.platform === 'win32' ? 'win' : process.platform === 'darwin' ? (process.arch === 'arm64' ? 'mac' : 'macx64') : 'linux';
const targets = process.argv.slice(2).length ? process.argv.slice(2) : [here];
const version = require('../package.json').version;
const T = {
  win:    { dir: 'win',       exe: 'velodesk.exe', goos: 'windows', goarch: 'amd64' },
  linux:  { dir: 'linux',     exe: 'velodesk',     goos: 'linux',   goarch: 'amd64' },
  mac:    { dir: 'mac-arm64', exe: 'velodesk',     goos: 'darwin',  goarch: 'arm64' },
  macx64: { dir: 'mac-x64',   exe: 'velodesk',     goos: 'darwin',  goarch: 'amd64' },
};

for (const name of targets) {
  const t = T[name];
  if (!t) { console.error('alvo desconhecido: ' + name); process.exit(1); }
  const dir = path.join(__dirname, '..', 'resources', t.dir);
  fs.mkdirSync(dir, { recursive: true });
  execFileSync('go', ['build', '-trimpath', '-ldflags', `-s -w -X main.version=${version}`, '-o', path.join(dir, t.exe), './cmd/velodesk'], {
    cwd: path.join(__dirname, '..', '..', 'core'),
    stdio: 'inherit',
    env: { ...process.env, CGO_ENABLED: '0', GOOS: t.goos, GOARCH: t.goarch },
  });
  const def = process.env.VELODESK_DEFAULT_SERVER, file = path.join(dir, 'default-server.txt');
  const lock = path.join(dir, 'server-locked.txt');
  if (def) fs.writeFileSync(file, def + '\n'); else fs.rmSync(file, { force: true });
  if (def && process.env.VELODESK_LOCK_SERVER === '1') fs.writeFileSync(lock, 'locked\n'); else fs.rmSync(lock, { force: true });   // sem servidor embutido, o app pede na primeira vez
  console.log('núcleo', name, '->', path.join(dir, t.exe));
}
