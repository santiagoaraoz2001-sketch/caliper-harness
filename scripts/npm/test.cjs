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
async function fixture(t, platform = app.identity) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'caliper-npm-test-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const root = path.join(dir, 'source', 'Caliper'); fs.mkdirSync(path.join(root, 'runtime'), { recursive: true });
  fs.writeFileSync(path.join(root, 'prompts.yaml'), 'original instructions');
  fs.writeFileSync(path.join(root, 'runtime', 'manifest.json'), JSON.stringify({ format: 1, platform }));
  fs.writeFileSync(app.executable(root), '#!/bin/sh\nprintf "cwd=%s\\narg=%s\\n" "$PWD" "$1"\nexit 7\n', { mode: 0o755 });
  for (const name of ['MANIFEST.sha256', 'runtime/decision/requirements.lock', 'bin/rg', 'bin/rg.exe', 'runtime/decision/bin/python3', 'runtime/worker/bin/python3', 'runtime/decision/python.exe', 'kuzu_shared.dll', 'ensure-windows-runtime.ps1', 'lib/libkuzu.dylib', 'lib/libkuzu.so']) { const file = path.join(root, name); fs.mkdirSync(path.dirname(file), { recursive: true }); fs.writeFileSync(file, 'fixture component', { mode: 0o755 }); }
  if (platform.startsWith("windows-")) fs.writeFileSync(path.join(root,"caliper.exe"),"binary fixture");
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
function platformApp(platform, spawn, env = {}) {
  const load = name => name === 'node:child_process' ? {spawn} : require(name);
  const sandbox = {require: load, process: {platform, arch: 'x64', env: {...process.env, SystemRoot: 'C:\\Windows', ...env}}, module: {exports: {}}, console};
  require('node:vm').runInNewContext(fs.readFileSync(path.join(__dirname,'install.cjs'),'utf8'),sandbox);
  return sandbox.module.exports;
}
function windowsApp(spawn) { return platformApp('win32', spawn); }
function simulatedSpawn(codes, calls) {
  return (file,args,options) => {
    calls.push({file,args,options});
    const child = new (require('node:events').EventEmitter)();
    setImmediate(() => child.emit('exit', codes.shift())); return child;
  };
}
test('Windows bundles require system prerequisite helper instead of bundled VC DLLs', async t => {
  const f=await fixture(t), root=path.join(f.dir,'source','Caliper');
  fs.writeFileSync(path.join(root,'caliper.exe'),'binary fixture');
  fs.writeFileSync(path.join(root,'runtime','manifest.json'),JSON.stringify({format:1,platform:'windows-amd64'}));
  const win = windowsApp(() => {throw Error('validation must not execute')});
  win.validate(root);
  fs.rmSync(path.join(root,'ensure-windows-runtime.ps1'));
  assert.throws(()=>win.validate(root), /ensure-windows-runtime/);
  assert.throws(()=>win.validate(root,true), /ensure-windows-runtime/);
});
test('Windows prerequisite failure during real install preserves previous selection', async t => {
  const f=await fixture(t,'windows-amd64');
  for(const failure of [1,3010]) {
    const base=path.join(f.dir,`failed-${failure}`); fs.mkdirSync(base);
    const receipt=path.join(base,'current.json'); fs.writeFileSync(receipt,'previous selection');
    const calls=[], win=windowsApp(simulatedSpawn([failure],calls));
    await assert.rejects(win.install(base,f.download), /prerequisite failed/);
    assert.equal(calls.length,1); assert.match(calls[0].file,/System32.*powershell.exe$/);
    assert.equal(calls[0].args.at(-1),path.join(base,'versions',app.version,'windows-amd64','Caliper','ensure-windows-runtime.ps1'));
    assert.equal(calls[0].options.cwd,undefined); assert.equal(calls[0].options.stdio,'inherit');
    assert.equal(fs.readFileSync(receipt,'utf8'),'previous selection');
  }
  const calls=[], win=windowsApp(simulatedSpawn([0,0],calls));
  await win.activateNative(path.join(f.dir,'source','Caliper'));
  assert.equal(calls.length,2); assert.match(calls[1].file,/caliper.exe$/);
  assert.deepEqual(Array.from(calls[1].args),['install-activate']);
});
test('legacy Windows selection requires inventoried matching app-local DLLs', async t => {
  const f=await fixture(t), root=path.join(f.dir,'source','Caliper');
  fs.writeFileSync(path.join(root,'caliper.exe'),'binary fixture');
  fs.writeFileSync(path.join(root,'runtime','manifest.json'),JSON.stringify({format:1,platform:'windows-amd64'}));
  fs.rmSync(path.join(root,'ensure-windows-runtime.ps1'));
  const names=['msvcp140.dll','vcruntime140.dll','vcruntime140_1.dll','runtime/decision/msvcp140.dll'];
  for(const name of names) {
    fs.writeFileSync(path.join(root,name),'original legacy DLL');
    fs.appendFileSync(path.join(root,'MANIFEST.sha256'),`${crypto.createHash('sha256').update('original legacy DLL').digest('hex')}  ./${name}\n`);
  }
  const win=windowsApp(()=>{throw Error('legacy app-local runtime needs no system install')});
  const inventory=fs.readFileSync(path.join(root,'MANIFEST.sha256'),'utf8');
  for(const ending of ['\n','\r\n']) for(const finalNewline of [true,false]) {
    const layout=inventory.replace(/\n$/,'').replace(/\n/g,ending)+(finalNewline?ending:'');
    fs.writeFileSync(path.join(root,'MANIFEST.sha256'),layout);
    fs.writeFileSync(path.join(root,names[0]),'original legacy DLL');
    win.validate(root,true); await win.ensurePrerequisites(root);
    assert.throws(()=>win.validate(root),/ensure-windows-runtime/);
    fs.writeFileSync(path.join(root,names[0]),'tampered');
    assert.throws(()=>win.validate(root,true),/ensure-windows-runtime/);
  }
});
test('Windows launcher invokes prerequisite before the binary and does not forward user arguments to installer', async t => {
  const f=await fixture(t), root=path.join(f.dir,'source','Caliper'), calls=[];
  const base=path.dirname(root), selected={version:'0.1.2',platform:'windows-amd64',root};
  const wrapper={home:()=>base, identity:selected.platform, validate:()=>calls.push('validate'), ensurePrerequisites:async()=>calls.push('prerequisite'), executable:()=>path.join(root,'caliper.exe')};
  // Use a real selection path, with fs intercepted only for the receipt.
  selected.root=path.join(base,'versions',selected.version,selected.platform,'Caliper');
  const load=name=> name==='./install.cjs'?wrapper:name==='node:fs'?{readFileSync:()=>JSON.stringify(selected)}:name==='node:child_process'?{spawn:(file,args,options)=>{calls.push({file,args,options});return new (require('node:events').EventEmitter)();}}:require(name);
  const processFixture={platform:'win32',env:{},argv:['node','bin','--config','path with spaces.yaml'],on:()=>{}};
  require('node:vm').runInNewContext(fs.readFileSync(path.join(__dirname,'bin.cjs'),'utf8'),{require:load,process:processFixture,console});
  await new Promise(setImmediate);
  assert.equal(calls[0],'validate'); assert.equal(calls[1],'prerequisite');
  assert.deepEqual(Array.from(calls[2].args),['--config','path with spaces.yaml']); assert.equal(calls[2].options.cwd,undefined);
});
test('plain npm registration invokes the packaged installer with literal paths and inherited cwd', async () => {
  for (const platform of ['darwin','linux','win32']) {
    const calls=[], client=platformApp(platform,simulatedSpawn([0],calls),{npm_config_global:'false'});
    const root=path.resolve("Caliper app's folder");
    await client.registerCommand(root);
    assert.equal(calls.length,1); const call=calls[0];
    assert.equal(call.options.cwd,undefined); assert.equal(call.options.stdio,'inherit');
    if(platform==='win32') {
      assert.match(call.file,/System32.*WindowsPowerShell.*powershell.exe$/);
      assert.deepEqual(Array.from(call.args),['-NoProfile','-ExecutionPolicy','Bypass','-File',path.join(root,'install-windows.ps1'),'-TargetDirectory',root]);
    } else {
      assert.equal(call.file,'/bin/sh'); assert.deepEqual(Array.from(call.args),[path.join(root,'install.sh')]);
    }
  }
});
test('global npm installation keeps npm-owned command registration', async () => {
  for(const platform of ['darwin','linux','win32']) {
    const client=platformApp(platform,()=>{throw Error('global install must not launch the platform installer');},{npm_config_global:'true'});
    await client.registerCommand('/unused/app');
  }
});
test('registration failures are returned instead of reporting an available command', async () => {
  for(const platform of ['darwin','win32']) {
    const calls=[], client=platformApp(platform,simulatedSpawn([7],calls),{npm_config_global:'false'});
    await assert.rejects(client.registerCommand('/app with spaces'));
    assert.equal(calls.length,1);
    const failed=platformApp(platform,()=>{
      const child=new (require('node:events').EventEmitter)();setImmediate(()=>child.emit('error',new Error('installer could not start')));return child;
    },{npm_config_global:'false'});
    await assert.rejects(failed.registerCommand('/app'),/installer could not start/);
  }
});
test('plain npm registration uses real Unix installer and creates a working future-shell command', {skip:process.platform==='win32'}, async t => {
  const f=await fixture(t), root=path.join(f.dir,"Caliper app's folder");
  fs.renameSync(path.join(f.dir,'source','Caliper'),root);
  fs.copyFileSync(path.join(__dirname,'..','install.sh'),path.join(root,'install.sh'));
  const home=path.join(f.dir,"user home's path"), cwd=path.join(f.dir,'unrelated project');
  fs.mkdirSync(home);fs.mkdirSync(cwd);
  const env={...process.env,HOME:home,SHELL:'/bin/sh',npm_config_global:'false'};
  const script='require(process.argv[1]).registerCommand(process.argv[2]).catch(e=>{console.error(e);process.exitCode=1})';
  const register=spawnSync(process.execPath,['-e',script,path.join(__dirname,'install.cjs'),root],{cwd,env,encoding:'utf8'});
  assert.equal(register.status,0,register.stderr);
  const launcher=path.join(home,'.local','bin','caliper'); assert.ok(fs.existsSync(launcher));
  const launched=spawnSync('/bin/sh',['-c','. "$HOME/.profile"; command -v caliper; caliper "$1"','--','literal argument with spaces'],{cwd,env,encoding:'utf8'});
  assert.equal(launched.status,7,launched.stderr);
  assert.ok(launched.stdout.includes(launcher)); assert.ok(launched.stdout.includes(`cwd=${fs.realpathSync(cwd)}`));
  assert.match(launched.stdout,/arg=literal argument with spaces/);assert.deepEqual(fs.readdirSync(cwd),[]);
  // Repeating npm install should update the owned launcher without duplicating PATH entries.
  const again=spawnSync(process.execPath,['-e',script,path.join(__dirname,'install.cjs'),root],{cwd,env,encoding:'utf8'});
  assert.equal(again.status,0,again.stderr);
  assert.equal(fs.readFileSync(path.join(home,'.profile'),'utf8').split('# Caliper terminal command').length-1,1);
});
