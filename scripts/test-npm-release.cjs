// Exercise the real native release through the npm installer and command shim.
// Usage: node scripts/test-npm-release.cjs ARCHIVE NEW_EVIDENCE_DIRECTORY
'use strict';
const fs = require('node:fs');
const path = require('node:path');
const { Readable } = require('node:stream');
const { spawnSync } = require('node:child_process');
const app = require('./npm/install.cjs');
(async () => {
 const archive = path.resolve(process.argv[2]); const evidence = path.resolve(process.argv[3]); fs.mkdirSync(evidence);
 const user = path.join(evidence, 'home'); fs.mkdirSync(user); const base = process.platform === 'darwin' ? path.join(user,'Library','Application Support','caliper') : process.platform === 'win32' ? path.join(user,'AppData','Local','caliper') : path.join(user,'.local','share','caliper');
 Object.assign(process.env,{HOME:user,USERPROFILE:user,LOCALAPPDATA:path.join(user,'AppData','Local'),XDG_DATA_HOME:path.join(user,'.local','share'),ZDOTDIR:user,SHELL:'/bin/sh'});
 delete process.env.npm_config_global;
 const split = archive.endsWith('.parts.json');
 const whole = split ? archive.slice(0, -'.parts.json'.length) : archive;
 const read = async url => {
  let file = url.endsWith('.sha256') ? whole+'.sha256' : url.endsWith('.parts.json') ? whole+'.parts.json' : url.includes('.part-') ? path.join(path.dirname(whole), path.basename(new URL(url).pathname)) : whole;
  if (!fs.existsSync(file)) { const error = new Error('Not found'); error.status=404; throw error; }
  return new Response(Readable.toWeb(fs.createReadStream(file)));
 };
 const root = await app.install(base,read);
 const project = path.join(evidence,'unrelated project');fs.mkdirSync(project);fs.writeFileSync(path.join(project,'keep.txt'),'owner work');
 const env = {...process.env};
 const result=spawnSync(process.execPath,[path.resolve(__dirname,'npm/bin.cjs'),'version'],{cwd:project,env,encoding:'utf8'});
 const windows = process.platform === 'win32';
 const powershell = windows && path.join(process.env.SystemRoot,'System32','WindowsPowerShell','v1.0','powershell.exe');
 let originalUserPath;
 if (windows) {
  const saved=spawnSync(powershell,['-NoProfile','-Command',"[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false); ConvertTo-Json -Compress -InputObject ([Environment]::GetEnvironmentVariable('Path','User'))"],{encoding:'utf8'});
  if(saved.status!==0)throw new Error(`Cannot preserve Windows user PATH: ${saved.stderr}`);
  originalUserPath=JSON.parse(saved.stdout);
 }
 try {
  // Exercise the real postinstall entrypoint against the already installed
  // version, including verify/activate/register, without a network fetch.
  const postinstall=spawnSync(process.execPath,[path.resolve(__dirname,'npm/install.cjs')],{cwd:project,env:{...env,npm_config_global:'false'},encoding:'utf8'});
  if(postinstall.status!==0)throw new Error(`Postinstall command registration failed: ${postinstall.stderr}`);
  const command=windows ? path.join(root,'commands','caliper.cmd') : path.join(user,'.local','bin','caliper');
  const runRegistered = args => windows
   ? spawnSync(path.join(process.env.SystemRoot,'System32','cmd.exe'),['/d','/s','/c',`""${command}" ${args.map(arg=>`"${arg}"`).join(' ')}"`],{cwd:project,env,encoding:'utf8',windowsVerbatimArguments:true})
   : spawnSync(command,args,{cwd:project,env,encoding:'utf8'});
  const registered=runRegistered(['version']);
  // A relative config is an observable cwd proof; version alone cannot detect
  // a launcher that silently changes into the application directory.
  const configName='cwd proof.yaml';
  fs.writeFileSync(path.join(project,configName),'model:\n  name: caliper-cwd-proof\n');
  let cwd;
  try { cwd=runRegistered(['config','--config',configName]); }
  finally { fs.unlinkSync(path.join(project,configName)); }
  const projectFiles=fs.readdirSync(project);
  fs.writeFileSync(path.join(evidence,'RESULT.json'),JSON.stringify({archive,exit:result.status,stdout:result.stdout,stderr:result.stderr,postinstall:{exit:postinstall.status,stdout:postinstall.stdout,stderr:postinstall.stderr},registered:{command,exit:registered.status,stdout:registered.stdout,stderr:registered.stderr},cwd:{exit:cwd.status,stdout:cwd.stdout,stderr:cwd.stderr},projectFiles,selection:JSON.parse(fs.readFileSync(path.join(base,'current.json'),'utf8'))},null,2));
  if(result.status!==0||result.stdout.trim()!==app.version||registered.status!==0||registered.stdout.trim()!==app.version||cwd.status!==0||!cwd.stdout.includes('name: caliper-cwd-proof')||projectFiles.join()!=='keep.txt')throw new Error('Native release launch/registration/cwd/preservation failed; see RESULT.json');
 } finally {
  if (windows) {
   const restored=spawnSync(powershell,['-NoProfile','-Command',"$saved = ConvertFrom-Json $env:CALIPER_TEST_ORIGINAL_USER_PATH_JSON; [Environment]::SetEnvironmentVariable('Path',$saved,'User')"],{env:{...process.env,CALIPER_TEST_ORIGINAL_USER_PATH_JSON:JSON.stringify(originalUserPath)},encoding:'utf8'});
   if(restored.status!==0)throw new Error(`Cannot restore Windows test user PATH: ${restored.stderr}`);
  }
 }
 console.log(`PASS native npm whole-product install and command: ${app.version}`);
})().catch(e=>{console.error(e);process.exitCode=1});
