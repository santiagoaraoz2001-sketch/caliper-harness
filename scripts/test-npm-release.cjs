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
 Object.assign(process.env,{HOME:user,USERPROFILE:user,LOCALAPPDATA:path.join(user,'AppData','Local'),XDG_DATA_HOME:path.join(user,'.local','share')});
 const split = archive.endsWith('.parts.json');
 const whole = split ? archive.slice(0, -'.parts.json'.length) : archive;
 const read = async url => {
  let file = url.endsWith('.sha256') ? whole+'.sha256' : url.endsWith('.parts.json') ? whole+'.parts.json' : url.includes('.part-') ? path.join(path.dirname(whole), path.basename(new URL(url).pathname)) : whole;
  if (!fs.existsSync(file)) { const error = new Error('Not found'); error.status=404; throw error; }
  return new Response(Readable.toWeb(fs.createReadStream(file)));
 };
 await app.install(base,read);
 const project = path.join(evidence,'unrelated project');fs.mkdirSync(project);fs.writeFileSync(path.join(project,'keep.txt'),'owner work');
 const env = {...process.env,HOME:user,USERPROFILE:user,LOCALAPPDATA:path.join(user,'AppData','Local'),XDG_DATA_HOME:path.join(user,'.local','share')};
 const result=spawnSync(process.execPath,[path.resolve(__dirname,'npm/bin.cjs'),'version'],{cwd:project,env,encoding:'utf8'});
 fs.writeFileSync(path.join(evidence,'RESULT.json'),JSON.stringify({archive,exit:result.status,stdout:result.stdout,stderr:result.stderr,projectFiles:fs.readdirSync(project),selection:JSON.parse(fs.readFileSync(path.join(base,'current.json'),'utf8'))},null,2));
 if(result.status!==0||result.stdout.trim()!==app.version||fs.readdirSync(project).join()!=='keep.txt')throw new Error('Native release launch/preservation failed; see RESULT.json');
 console.log(`PASS native npm whole-product install and command: ${app.version}`);
})().catch(e=>{console.error(e);process.exitCode=1});
