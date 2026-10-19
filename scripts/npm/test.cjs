'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const tar = require('tar');
const { spawnSync } = require('node:child_process');
const app = require('./install.cjs');
const fixtureActivate = async (root, version, base) => fs.writeFileSync(path.join(base,'current.json'),JSON.stringify({root,version,platform:app.identity}));
async function fixture(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'caliper-npm-test-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const root = path.join(dir, 'source', 'Caliper'); fs.mkdirSync(path.join(root, 'runtime'), { recursive: true });
  fs.writeFileSync(path.join(root, 'prompts.yaml'), 'original instructions');
  fs.writeFileSync(path.join(root, 'runtime', 'manifest.json'), JSON.stringify({ format: 1, platform: app.identity }));
  fs.writeFileSync(app.executable(root), '#!/bin/sh\nprintf "cwd=%s\\narg=%s\\n" "$PWD" "$1"\nexit 7\n', { mode: 0o755 });
  for (const name of ['MANIFEST.sha256', 'runtime/decision/requirements.lock', 'bin/rg', 'bin/rg.exe', 'runtime/decision/bin/python3', 'runtime/worker/bin/python3', 'runtime/decision/python.exe', 'runtime/decision/msvcp140.dll', 'kuzu_shared.dll', 'msvcp140.dll', 'vcruntime140.dll', 'vcruntime140_1.dll', 'lib/libkuzu.dylib', 'lib/libkuzu.so']) { const file = path.join(root, name); fs.mkdirSync(path.dirname(file), { recursive: true }); fs.writeFileSync(file, 'fixture component', { mode: 0o755 }); }
  const inventory = [];
  function walk(dir) { for (const item of fs.readdirSync(dir, { withFileTypes: true })) { const file = path.join(dir, item.name); if (item.isDirectory()) walk(file); else if (item.name !== 'MANIFEST.sha256') inventory.push(`${crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex')}  ./${path.relative(root, file).split(path.sep).join('/')}`); } }
  walk(root); fs.writeFileSync(path.join(root, 'MANIFEST.sha256'), inventory.join('\n') + '\n');
  const archive = path.join(dir, 'bundle.tar.gz'); await tar.c({ gzip: true, cwd: path.dirname(root), file: archive }, ['Caliper']);
  const data = fs.readFileSync(archive); const hash = crypto.createHash('sha256').update(data).digest('hex');
  const download = async url => new Response(url.endsWith('.sha256') ? `${hash}  bundle.tar.gz` : data);
  return { dir, data, download };
}
test('whole-folder install preserves user files and explicit reinstall selects npm version', async t => {
  const f = await fixture(t); const home = path.join(f.dir, 'user-data'); fs.mkdirSync(home); fs.writeFileSync(path.join(home, 'settings.yaml'), 'user settings');
  const root = await app.install(home, f.download, fixtureActivate); assert.equal(fs.readFileSync(path.join(home, 'settings.yaml'), 'utf8'), 'user settings');
  assert.equal(JSON.parse(fs.readFileSync(path.join(home, 'current.json'))).root, root);
  fs.writeFileSync(path.join(home, 'current.json'), JSON.stringify({ version: '0.9.0' }));
  await app.install(home, async () => { throw new Error('existing version should be reused'); }, fixtureActivate);
  assert.equal(JSON.parse(fs.readFileSync(path.join(home, 'current.json'))).version, app.version);
});
test('checksum failure preserves old selection and leaves no completed install', async t => {
  const f = await fixture(t); const home = path.join(f.dir, 'user-data'); fs.mkdirSync(home); fs.writeFileSync(path.join(home, 'current.json'), 'prior receipt');
  await assert.rejects(app.install(home, async url => new Response(url.endsWith('.sha256') ? '0'.repeat(64) : f.data), fixtureActivate), /checksum mismatch/);
  assert.equal(fs.readFileSync(path.join(home, 'current.json'), 'utf8'), 'prior receipt');
  assert.deepEqual(fs.readdirSync(path.join(home, 'versions')), []);
});
test('launcher preserves cwd, argument and exit status without project debris', { skip: process.platform === 'win32' }, async t => {
  const f = await fixture(t); const user = path.join(f.dir, 'home'); fs.mkdirSync(user);
  const base = process.platform === 'darwin' ? path.join(user, 'Library', 'Application Support', 'caliper') : path.join(user, '.local', 'share', 'caliper');
  await app.install(base, f.download, fixtureActivate); const cwd = path.join(f.dir, 'project with spaces'); fs.mkdirSync(cwd);
  const result = spawnSync(process.execPath, [path.join(__dirname, 'bin.cjs'), 'literal arg with spaces'], { cwd, encoding: 'utf8', env: { ...process.env, HOME: user, XDG_DATA_HOME: path.join(user, '.local', 'share') } });
  assert.equal(result.status, 7, result.stderr); assert.match(result.stdout, /arg=literal arg with spaces/); assert.ok(result.stdout.includes(`cwd=${fs.realpathSync(cwd)}`)); assert.deepEqual(fs.readdirSync(cwd), []);
});
test('split archive transport reconstructs complete product and rejects bad parts', async t => {
  const f = await fixture(t); const name = `caliper-${app.version}-${app.identity}.tar.gz`; const split = Math.floor(f.data.length / 2); const chunks = [f.data.subarray(0, split), f.data.subarray(split)];
  const manifest = { format: 1, archive: name, sha256: crypto.createHash('sha256').update(f.data).digest('hex'), parts: chunks.map((data, i) => ({ name: `${name}.part-${String(i).padStart(3, '0')}`, bytes: data.length, sha256: crypto.createHash('sha256').update(data).digest('hex') })) };
  for (const corrupt of [false, true]) {
    const home = path.join(f.dir, `split-${corrupt}`);
    const download = async url => {
      if (url.endsWith('.sha256')) return new Response(manifest.sha256);
      if (url.endsWith('.parts.json')) return new Response(JSON.stringify(manifest));
      const index = manifest.parts.findIndex(part => url.endsWith(part.name));
      if (index >= 0) return new Response(corrupt && index === 1 ? 'broken' : chunks[index]);
      const error = new Error('Not found'); error.status = 404; throw error;
    };
    if (corrupt) { await assert.rejects(app.install(home, download, fixtureActivate), /part failed verification/); assert.equal(fs.existsSync(path.join(home,'current.json')), false); }
    else { const root = await app.install(home, download, fixtureActivate); app.validate(root); }
  }
});
test('nonexecutable installed commands cannot be activated on Unix', { skip: process.platform === 'win32' }, async t => {
  const f=await fixture(t);const home=path.join(f.dir,'mode-home');const root=await app.install(home,f.download,fixtureActivate);
  const prior=fs.readFileSync(path.join(home,'current.json'),'utf8');fs.chmodSync(app.executable(root),0o600);
  await assert.rejects(app.install(home,f.download,fixtureActivate),/EACCES/);
  assert.equal(fs.readFileSync(path.join(home,'current.json'),'utf8'),prior);
});
test('Windows selection requires all app-local VC runtime DLLs', async t => {
  const f=await fixture(t); const root=path.join(f.dir,'source','Caliper');
  fs.writeFileSync(path.join(root,'caliper.exe'),'binary fixture');
  fs.writeFileSync(path.join(root,'runtime','manifest.json'),JSON.stringify({format:1,platform:'windows-amd64'}));
  // Exercise the Windows validation branch on every host without executing a
  // foreign binary; the native release proof separately launches the real exe.
  const sandbox={require,process:{platform:'win32',arch:'x64'},module:{exports:{}},console};
  require('node:vm').runInNewContext(fs.readFileSync(path.join(__dirname,'install.cjs'),'utf8'),sandbox);
  const validate=sandbox.module.exports.validate;validate(root);
  for(const name of ['msvcp140.dll','vcruntime140.dll','vcruntime140_1.dll','runtime/decision/msvcp140.dll']) {
    const file=path.join(root,name);fs.renameSync(file,file+'.absent');
    assert.throws(()=>validate(root),new RegExp(path.basename(name).replaceAll('.','\\.')));
    fs.renameSync(file+'.absent',file);
  }
});
