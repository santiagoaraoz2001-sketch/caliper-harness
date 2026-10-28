'use strict';
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const { pipeline, finished } = require('node:stream/promises');
const { Transform, Readable } = require('node:stream');
const tar = require('tar');
const { spawn } = require('node:child_process');
const version = require('./package.json').version;
const identity = `${process.platform === 'win32' ? 'windows' : process.platform}-${process.arch === 'x64' ? 'amd64' : process.arch}`;
const supported = ['darwin-arm64', 'windows-amd64', 'linux-amd64', 'linux-arm64'];
function home() {
  if (process.platform === 'darwin') return path.join(os.homedir(), 'Library', 'Application Support', 'caliper');
  if (process.platform === 'win32') return path.join(process.env.LOCALAPPDATA || process.env.APPDATA, 'caliper');
  return path.join(process.env.XDG_DATA_HOME || path.join(os.homedir(), '.local', 'share'), 'caliper');
}
function executable(root) { return path.join(root, process.platform === 'win32' ? 'caliper.exe' : 'caliper-harness'); }
function validate(root, allowLegacy = false) {
  if (fs.existsSync(path.join(root, 'BUILD-INCOMPLETE'))) throw new Error('Incomplete application bundle');
  const required = ['prompts.yaml', 'MANIFEST.sha256', 'runtime/decision/requirements.lock'];
  if (identity.startsWith('windows-')) { required.push('kuzu_shared.dll', 'bin/rg.exe', 'runtime/decision/python.exe'); if (!allowLegacy || !legacyWindowsRuntime(root)) required.push('ensure-windows-runtime.ps1'); }
  else { required.push('bin/rg', 'runtime/decision/bin/python3', identity.startsWith('darwin-') ? 'lib/libkuzu.dylib' : 'lib/libkuzu.so'); if (identity.startsWith('darwin-')) required.push('runtime/worker/bin/python3'); }
  for (const name of [executable(root), ...required.map(file => path.join(root, file))]) if (!fs.statSync(name).isFile()) throw new Error(`Missing product file: ${name}`);
  if (process.platform !== 'win32') { for (const file of [executable(root), ...required.filter(name => name === 'bin/rg' || name.endsWith('/bin/python3')).map(name => path.join(root,name))]) fs.accessSync(file, fs.constants.X_OK); }
  const manifest = JSON.parse(fs.readFileSync(path.join(root, 'runtime', 'manifest.json'), 'utf8'));
  if (manifest.format !== 1 || manifest.platform !== identity) throw new Error('Application runtime platform mismatch');
}
// One bounded observation per second, plus phase boundaries. Byte totals are
// transport facts, never overall installation percentages or an ETA.
function progressReporter(notify, now = Date.now) {
  const started = now(); let last = -Infinity, value = {};
  return (change, force = false) => {
    const changedPhase = change.phase && change.phase !== value.phase;
    value = { ...value, ...change };
    const time = now();
    if (!force && !changedPhase && time - last < 1000) return;
    last = time; notify({ ...value, elapsedMs: Math.max(0, time - started) });
  };
}
let progressWidth = 0;
function showProgress(p) {
  let line = `Caliper: ${p.phase}`;
  if (p.phase === 'Downloading update') {
    line += ` ${(p.bytes / 1048576).toFixed(1)} MiB`;
    if (p.totalBytes > 0) line += ` / ${(p.totalBytes / 1048576).toFixed(1)} MiB`;
  }
  if (p.phase === 'Verifying installed files' && p.totalFiles) line += ` ${p.files}/${p.totalFiles} files`;
  line += ` (${Math.round(p.elapsedMs / 1000)}s elapsed)`;
  if (process.stderr.isTTY) {
    process.stderr.write('\r' + line.padEnd(progressWidth)); progressWidth = line.length;
    if (p.phase === 'Ready') { process.stderr.write('\n'); progressWidth = 0; }
  } else process.stderr.write(line + '\n');
}
async function verify(root, report = () => {}) {
  report({phase: 'Verifying installed files', files: 0, totalFiles: 0});
  validate(root);
  const lines = fs.readFileSync(path.join(root, 'MANIFEST.sha256'), 'utf8').trim().split(/\r?\n/);
  let files = 0;
  for (const line of lines) {
    const match = /^([a-f0-9]{64})  \.\/(.+)$/i.exec(line);
    if (!match || /[\\:\0]/.test(match[2])) throw new Error('Invalid application inventory');
    const file = fs.realpathSync(path.resolve(root, match[2]));
    const rel = path.relative(fs.realpathSync(root), file);
    if (rel === '..' || rel.startsWith('..' + path.sep) || path.isAbsolute(rel)) throw new Error('Application inventory escapes install');
    const hash = crypto.createHash('sha256');
    for await (const chunk of fs.createReadStream(file)) hash.update(chunk);
    if (hash.digest('hex').toLowerCase() !== match[1].toLowerCase()) throw new Error(`Application inventory mismatch: ${match[2]}`);
    files++; report({files, totalFiles: lines.length}, files === lines.length);
  }
}
async function get(url) { const r = await fetch(url); if (!r.ok) { const error = new Error(`${url}: HTTP ${r.status}`); error.status = r.status; throw error; } return r; }
function legacyWindowsRuntime(root) {
  try {
    const inventory = '\n' + fs.readFileSync(path.join(root, 'MANIFEST.sha256'), 'utf8').replace(/\r\n/g, '\n').replace(/\n$/, '') + '\n';
    return ['msvcp140.dll', 'vcruntime140.dll', 'vcruntime140_1.dll', 'runtime/decision/msvcp140.dll'].every(name => {
      const file = fs.realpathSync(path.join(root, name)), rel = path.relative(fs.realpathSync(root), file);
      if (rel === '..' || rel.startsWith('..' + path.sep) || path.isAbsolute(rel)) return false;
      const hash = crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
      return inventory.includes(`\n${hash}  ./${name}\n`);
    });
  } catch { return false; }
}
// Windows PowerShell 5.1 must never autoload PowerShell 7 modules: a parent
// pwsh session exports its own PSModulePath to children, and 5.1 then fails
// loading Core-edition modules such as Microsoft.PowerShell.Security
// (Get-AuthenticodeSignature). Spawn powershell.exe with the Windows
// PowerShell module defaults instead of the inherited module path.
function windowsPowerShellEnv() {
  const profile = process.env.USERPROFILE || os.homedir();
  const programFiles = process.env.ProgramFiles || 'C:\\Program Files';
  const systemRoot = process.env.SystemRoot || 'C:\\Windows';
  const PSModulePath = [profile + '\\Documents\\WindowsPowerShell\\Modules', programFiles + '\\WindowsPowerShell\\Modules', systemRoot + '\\system32\\WindowsPowerShell\\v1.0\\Modules'].join(';');
  return { ...process.env, PSModulePath };
}
async function ensurePrerequisites(root) {
  if (process.platform !== 'win32') return;
  const helper = path.join(root, 'ensure-windows-runtime.ps1');
  if (!fs.existsSync(helper) && legacyWindowsRuntime(root)) return;
  if (!process.env.SystemRoot) throw new Error('SystemRoot unavailable; cannot check Windows runtime');
  await new Promise((resolve, reject) => {
    const child = spawn(path.join(process.env.SystemRoot, 'System32', 'WindowsPowerShell', 'v1.0', 'powershell.exe'), ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', helper], {env: windowsPowerShellEnv(), stdio: 'inherit', windowsHide: false});
    child.on('error', reject);
    child.on('exit', code => code === 0 ? resolve() : reject(new Error(`Windows runtime prerequisite failed (${code}); previous selection preserved. Finish any requested restart before retrying.`)));
  });
}
async function activateNative(root) {
  await ensurePrerequisites(root);
  const env = { ...process.env, PATH: path.join(root,'bin') + path.delimiter + (process.env.PATH || '') };
  if (process.platform === 'linux') env.LD_LIBRARY_PATH = path.join(root,'lib') + (process.env.LD_LIBRARY_PATH ? path.delimiter + process.env.LD_LIBRARY_PATH : '');
  await new Promise((resolve,reject) => {
    const child=spawn(executable(root),['install-activate'],{env,stdio:'inherit'});
    child.on('error',reject);child.on('exit',code=>code===0?resolve():reject(new Error(`Native installation activation failed (${code}); previous selection preserved`)));
  });
}
async function registerCommand(root) {
  // Global npm installs already register the package's bin entry. Local npm
  // installs need the same per-user registration as downloaded app folders.
  if (process.env.npm_config_global === 'true') return;
  let command = '/bin/sh', args = [path.join(root, 'install.sh')];
  if (process.platform === 'win32') {
    if (!process.env.SystemRoot) throw new Error('SystemRoot unavailable; cannot register Caliper command');
    command = path.join(process.env.SystemRoot, 'System32', 'WindowsPowerShell', 'v1.0', 'powershell.exe');
    // Register the installed folder in place; do not copy its runtime again.
    args = ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', path.join(root, 'install-windows.ps1'), '-TargetDirectory', root];
  }
  await new Promise((resolve, reject) => {
    const child = spawn(command, args, process.platform === 'win32' ? {env: windowsPowerShellEnv(), stdio: 'inherit', windowsHide: false} : {stdio: 'inherit', windowsHide: false});
    child.on('error', reject);
    child.on('exit', code => code === 0 ? resolve() : reject(new Error(`Caliper command registration failed (${code}); application retained at ${root}`)));
  });
}
async function install(base = home(), download = get, activate = activateNative, onProgress = require.main === module ? showProgress : () => {}) {
  const report = progressReporter(onProgress);
  report({phase: 'Preparing update', bytes: 0, totalBytes: 0});
  if (!supported.includes(identity)) throw new Error(`No Caliper product bundle for ${identity}. Supported: ${supported.join(', ')}`);
  const parent = path.join(base, 'versions', version, identity);
  const root = path.join(parent, 'Caliper');
  if (fs.existsSync(parent)) { await verify(root, report); report({phase: 'Activating verified update'}); await activate(root, version, base); report({phase: 'Ready'}); return root; }
  fs.mkdirSync(path.join(base, 'versions'), { recursive: true });
  const stage = fs.mkdtempSync(path.join(base, 'versions', '.npm-install-'));
  try {
    const name = `caliper-${version}-${identity}.tar.gz`;
    const url = `https://github.com/santiagoaraoz2001-sketch/caliper-harness/releases/download/v${version}/${name}`;
    report({phase: 'Fetching release checksum'});
    const checksum = (await (await download(`${url}.sha256`)).text()).trim().split(/\s+/)[0];
    if (!/^[a-f0-9]{64}$/i.test(checksum)) throw new Error('Invalid release checksum');
    const archive = path.join(stage, 'bundle.tar.gz');
    const hash = crypto.createHash('sha256');
    let downloaded = 0;
    const count = chunk => { downloaded += chunk.length; report({bytes: downloaded}); };
    const tee = new Transform({ transform(chunk, _, cb) { hash.update(chunk); count(chunk); cb(null, chunk); } });
    const output = fs.createWriteStream(archive, { flags: 'wx' });
    try {
      report({phase: 'Downloading update'});
      let response;
      try { response = await download(url); }
      catch (error) { if (error.status !== 404) throw error; }
      if (response) { const totalBytes = Number(response.headers?.get('content-length')); report({totalBytes: Number.isSafeInteger(totalBytes) && totalBytes > 0 ? totalBytes : 0}, true); await pipeline(Readable.fromWeb(response.body), tee, output); }
      else {
        const parts = await (await download(`${url}.parts.json`)).json();
        if (parts.format !== 1 || parts.archive !== name || parts.sha256.toLowerCase() !== checksum.toLowerCase() || !Array.isArray(parts.parts) || !parts.parts.length) throw new Error('Invalid split-archive manifest');
        const totalBytes = parts.parts.reduce((n, part) => Number.isSafeInteger(part.bytes) && part.bytes > 0 ? n + part.bytes : NaN, 0);
        report({totalBytes: Number.isSafeInteger(totalBytes) ? totalBytes : 0}, true);
        const seen = new Set();
        for (const part of parts.parts) {
          if (typeof part.name !== 'string' || path.basename(part.name) !== part.name || /[\\:]/.test(part.name) || !part.name.startsWith(name + '.part-') || seen.has(part.name) || !Number.isSafeInteger(part.bytes) || part.bytes <= 0 || !/^[a-f0-9]{64}$/i.test(part.sha256)) throw new Error('Invalid archive part');
          seen.add(part.name); const partHash = crypto.createHash('sha256'); let bytes = 0;
          const source = await download(new URL(part.name, url).href);
          const stream = new Transform({ transform(chunk, _, cb) { bytes += chunk.length; partHash.update(chunk); hash.update(chunk); count(chunk); cb(null, chunk); } });
          await pipeline(Readable.fromWeb(source.body), stream, output, { end: false });
          if (bytes !== part.bytes || partHash.digest('hex').toLowerCase() !== part.sha256.toLowerCase()) throw new Error(`Archive part failed verification: ${part.name}`);
        }
        output.end(); await finished(output);
      }
    } catch (error) { await new Promise(resolve => { if (output.closed) return resolve(); output.once("close", resolve); output.destroy(); }); throw error; }
    report({phase: 'Verifying archive checksum', bytes: downloaded});
    if (hash.digest('hex').toLowerCase() !== checksum.toLowerCase()) throw new Error('Release checksum mismatch; previous installation unchanged');
    const unpack = path.join(stage, 'unpacked'); fs.mkdirSync(unpack);
    report({phase: 'Extracting update'});
    await tar.x({ file: archive, cwd: unpack, strict: true, preservePaths: false });
    await verify(path.join(unpack, 'Caliper'), report);
    fs.mkdirSync(path.dirname(parent), { recursive: true });
    fs.renameSync(unpack, parent);
    report({phase: 'Activating verified update'});
    await activate(root, version, base);
    report({phase: 'Ready'});
    return root;
  } finally { fs.rmSync(stage, { recursive: true, force: true }); }
}
if (require.main === module) install().then(async root => { await registerCommand(root); console.log(`Caliper ${version} installed: ${root}\nRun caliper from your project directory. Reopen your terminal if the command is not found.`); }).catch(error => { console.error(`Caliper installation failed: ${error.message}`); process.exitCode = 1; });
module.exports = { home, executable, validate, install, identity, version, ensurePrerequisites, activateNative, registerCommand, progressReporter };
