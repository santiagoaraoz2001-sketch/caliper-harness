#!/usr/bin/env node
'use strict';
const fs = require('node:fs');
const path = require('node:path');
const { spawn } = require('node:child_process');
const app = require('./install.cjs');
(async () => {
  const base = app.home();
  let selected;
  try { selected = JSON.parse(fs.readFileSync(path.join(base, 'current.json'), 'utf8')); }
  catch (error) { if (error.code !== 'ENOENT') throw error; await app.install(); selected = JSON.parse(fs.readFileSync(path.join(base, 'current.json'), 'utf8')); }
  if (!/^\d+\.\d+\.\d+$/.test(selected.version) || selected.platform !== app.identity || selected.root !== path.join(base, 'versions', selected.version, app.identity, 'Caliper')) throw new Error('Invalid installed version selection');
  app.validate(selected.root, true);
  await app.ensurePrerequisites(selected.root);
  const env = { ...process.env, PATH: path.join(selected.root, 'bin') + path.delimiter + (process.env.PATH || '') };
  if (process.platform === 'linux') env.LD_LIBRARY_PATH = path.join(selected.root, 'lib') + (process.env.LD_LIBRARY_PATH ? path.delimiter + process.env.LD_LIBRARY_PATH : '');
  const child = spawn(app.executable(selected.root), process.argv.slice(2), { env, stdio: 'inherit', windowsHide: false });
  for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => { if (signal === 'SIGTERM') child.kill(signal); });
  child.on('error', error => { console.error(`Cannot start Caliper: ${error.message}`); process.exitCode = 1; });
  child.on('exit', (code, signal) => { process.exitCode = code ?? (signal === 'SIGINT' ? 130 : 1); });
})().catch(error => { console.error(`Caliper: ${error.message}`); process.exitCode = 1; });
