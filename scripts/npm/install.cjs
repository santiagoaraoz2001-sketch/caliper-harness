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
function validate(root) {
  if (fs.existsSync(path.join(root, 'BUILD-INCOMPLETE'))) throw new Error('Incomplete application bundle');
  const required = ['prompts.yaml', 'MANIFEST.sha256', 'runtime/decision/requirements.lock'];
  if (identity.startsWith('windows-')) required.push('kuzu_shared.dll', 'bin/rg.exe', 'runtime/decision/python.exe');
  else { required.push('bin/rg', 'runtime/decision/bin/python3', identity.startsWith('darwin-') ? 'lib/libkuzu.dylib' : 'lib/libkuzu.so'); if (identity.startsWith('darwin-')) required.push('runtime/worker/bin/python3'); }
  for (const name of [executable(root), ...required.map(file => path.join(root, file))]) if (!fs.statSync(name).isFile()) throw new Error(`Missing product file: ${name}`);
  if (process.platform !== 'win32') { for (const file of [executable(root), ...required.filter(name => name === 'bin/rg' || name.endsWith('/bin/python3')).map(name => path.join(root,name))]) fs.accessSync(file, fs.constants.X_OK); }
  const manifest = JSON.parse(fs.readFileSync(path.join(root, 'runtime', 'manifest.json'), 'utf8'));
  if (manifest.format !== 1 || manifest.platform !== identity) throw new Error('Application runtime platform mismatch');
}
async function verify(root) {
  validate(root);
  const lines = fs.readFileSync(path.join(root, 'MANIFEST.sha256'), 'utf8').trim().split(/\r?\n/);
  for (const line of lines) {
    const match = /^([a-f0-9]{64})  \.\/(.+)$/i.exec(line);
    if (!match || /[\\:\0]/.test(match[2])) throw new Error('Invalid application inventory');
    const file = fs.realpathSync(path.resolve(root, match[2]));
    const rel = path.relative(fs.realpathSync(root), file);
    if (rel === '..' || rel.startsWith('..' + path.sep) || path.isAbsolute(rel)) throw new Error('Application inventory escapes install');
    const hash = crypto.createHash('sha256');
    for await (const chunk of fs.createReadStream(file)) hash.update(chunk);
    if (hash.digest('hex').toLowerCase() !== match[1].toLowerCase()) throw new Error(`Application inventory mismatch: ${match[2]}`);
  }
}
async function get(url) { const r = await fetch(url); if (!r.ok) { const error = new Error(`${url}: HTTP ${r.status}`); error.status = r.status; throw error; } return r; }
async function activateNative(root) {
  const env = { ...process.env, PATH: path.join(root,'bin') + path.delimiter + (process.env.PATH || '') };
  if (process.platform === 'linux') env.LD_LIBRARY_PATH = path.join(root,'lib') + (process.env.LD_LIBRARY_PATH ? path.delimiter + process.env.LD_LIBRARY_PATH : '');
  await new Promise((resolve,reject) => {
    const child=spawn(executable(root),['install-activate'],{env,stdio:'inherit'});
    child.on('error',reject);child.on('exit',code=>code===0?resolve():reject(new Error(`Native installation activation failed (${code}); previous selection preserved`)));
  });
}
async function install(base = home(), download = get, activate = activateNative) {
  if (!supported.includes(identity)) throw new Error(`No Caliper product bundle for ${identity}. Supported: ${supported.join(', ')}`);
  const parent = path.join(base, 'versions', version, identity);
  const root = path.join(parent, 'Caliper');
  if (fs.existsSync(parent)) { await verify(root); await activate(root, version, base); return root; }
  fs.mkdirSync(path.join(base, 'versions'), { recursive: true });
  const stage = fs.mkdtempSync(path.join(base, 'versions', '.npm-install-'));
  try {
    const name = `caliper-${version}-${identity}.tar.gz`;
    const url = `https://github.com/santiagoaraoz2001-sketch/caliper-harness/releases/download/v${version}/${name}`;
    const checksum = (await (await download(`${url}.sha256`)).text()).trim().split(/\s+/)[0];
    if (!/^[a-f0-9]{64}$/i.test(checksum)) throw new Error('Invalid release checksum');
    const archive = path.join(stage, 'bundle.tar.gz');
    const hash = crypto.createHash('sha256');
    const tee = new Transform({ transform(chunk, _, cb) { hash.update(chunk); cb(null, chunk); } });
    const output = fs.createWriteStream(archive, { flags: 'wx' });
    try {
      let response;
      try { response = await download(url); }
      catch (error) { if (error.status !== 404) throw error; }
      if (response) await pipeline(Readable.fromWeb(response.body), tee, output);
      else {
        const parts = await (await download(`${url}.parts.json`)).json();
        if (parts.format !== 1 || parts.archive !== name || parts.sha256.toLowerCase() !== checksum.toLowerCase() || !Array.isArray(parts.parts) || !parts.parts.length) throw new Error('Invalid split-archive manifest');
        const seen = new Set();
        for (const part of parts.parts) {
          if (typeof part.name !== 'string' || path.basename(part.name) !== part.name || /[\\:]/.test(part.name) || !part.name.startsWith(name + '.part-') || seen.has(part.name) || !Number.isSafeInteger(part.bytes) || part.bytes <= 0 || !/^[a-f0-9]{64}$/i.test(part.sha256)) throw new Error('Invalid archive part');
          seen.add(part.name); const partHash = crypto.createHash('sha256'); let bytes = 0;
          const source = await download(new URL(part.name, url).href);
          const stream = new Transform({ transform(chunk, _, cb) { bytes += chunk.length; partHash.update(chunk); hash.update(chunk); cb(null, chunk); } });
          await pipeline(Readable.fromWeb(source.body), stream, output, { end: false });
          if (bytes !== part.bytes || partHash.digest('hex').toLowerCase() !== part.sha256.toLowerCase()) throw new Error(`Archive part failed verification: ${part.name}`);
        }
        output.end(); await finished(output);
      }
    } catch (error) { await new Promise(resolve => { if (output.closed) return resolve(); output.once("close", resolve); output.destroy(); }); throw error; }
    if (hash.digest('hex').toLowerCase() !== checksum.toLowerCase()) throw new Error('Release checksum mismatch; previous installation unchanged');
    const unpack = path.join(stage, 'unpacked'); fs.mkdirSync(unpack);
    await tar.x({ file: archive, cwd: unpack, strict: true, preservePaths: false });
    await verify(path.join(unpack, 'Caliper'));
    fs.mkdirSync(path.dirname(parent), { recursive: true });
    fs.renameSync(unpack, parent);
    await activate(root, version, base);
    return root;
  } finally { fs.rmSync(stage, { recursive: true, force: true }); }
}
if (require.main === module) install().then(root => console.log(`Caliper ${version} installed: ${root}\nRun caliper from your project directory.`)).catch(error => { console.error(`Caliper installation failed: ${error.message}`); process.exitCode = 1; });
module.exports = { home, executable, validate, install, identity, version };
