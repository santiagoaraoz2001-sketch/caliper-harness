'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const {validateRelease, registryState, newer, platforms} = require('./check-npm-release.cjs');
const sha = (bytes, algorithm = 'sha256', encoding = 'hex') => crypto.createHash(algorithm).update(bytes).digest(encoding);
function fixture() {
  const pkg = {name: 'caliper-harness', version: '0.1.2'};
  const lock = {...pkg, packages: {'': {...pkg}}};
  const release = {tag_name: 'v0.1.2', draft: false, prerelease: false, html_url: 'https://github.com/santiagoaraoz2001-sketch/caliper-harness/releases/tag/v0.1.2', assets: []};
  const metadata = new Map();
  function put(name, data) {
    const bytes = Buffer.from(data);
    metadata.set(name, bytes);
    release.assets = release.assets.filter(a => a.name !== name);
    release.assets.push({name, state: 'uploaded', size: bytes.length, digest: `sha256:${sha(bytes)}`});
  }
  for (const platform of platforms) {
    const archive = `caliper-0.1.2-${platform}.tar.gz`;
    const bytes = Buffer.from(`fixture archive for ${platform}`);
    put(`${archive}.sha256`, `${sha(bytes)}  ${archive}\n`);
    if (platform.startsWith('linux')) {
      const parts = [bytes.subarray(0, 10), bytes.subarray(10)].map((part, i) => {
        const name = `${archive}.part-${String(i).padStart(3, '0')}`;
        put(name, part); return {name, bytes: part.length, sha256: sha(part)};
      });
      put(`${archive}.parts.json`, JSON.stringify({format: 1, archive, sha256: sha(bytes), parts}));
    } else put(archive, bytes);
  }
  const validate = (dryRun = false) => validateRelease('v0.1.2', pkg, lock, release, name => metadata.get(name), dryRun);
  return {pkg, lock, release, metadata, put, validate};
}
test('complete four-platform inventory accepts single and split archives', () => {
  assert.equal(Object.keys(fixture().validate().transports).length, 4);
});
test('manual dry run accepts untagged draft; publication rejects draft and prerelease', () => {
  const f = fixture(); f.release.draft = true;
  f.release.html_url = 'https://github.com/santiagoaraoz2001-sketch/caliper-harness/releases/tag/untagged-123';
  assert.equal(f.validate(true).dryRun, true);
  assert.throws(() => f.validate(), /only dry runs/);
  f.release.prerelease = true; assert.throws(() => f.validate(true), /stable tag/);
});
test('release identity and both package identities must agree', () => {
  for (const modify of [f => f.pkg.version = '0.1.3', f => f.lock.packages[''].version = '0.1.3', f => f.release.tag_name = 'v0.1.3', f => f.release.html_url = 'https://github.com/other/repo/releases/tag/v0.1.2']) {
    const f = fixture(); modify(f); assert.throws(() => f.validate());
  }
  const f = fixture(); assert.throws(() => validateRelease('v0.1.2-beta', f.pkg, f.lock, f.release, () => {}, false), /stable/);
});
test('missing or incomplete platforms/parts and duplicates refuse', () => {
  for (const modify of [
    f => f.release.assets = f.release.assets.filter(a => !a.name.includes('windows')),
    f => f.release.assets = f.release.assets.filter(a => !a.name.endsWith('linux-arm64.tar.gz.part-001')),
    f => f.release.assets[0].state = 'new',
    f => f.release.assets.push(f.release.assets[0]),
    f => f.release.assets[0].digest = null,
  ]) { const f = fixture(); modify(f); assert.throws(() => f.validate()); }
});
test('downloaded metadata bytes must match GitHub size and digest', () => {
  const f = fixture(), name = f.release.assets[0].name;
  f.metadata.set(name, Buffer.from('modified')); assert.throws(() => f.validate(), /Downloaded metadata differs/);
});
test('archive checksum must match its name and GitHub digest', () => {
  for (const checksum of [`${'0'.repeat(64)}  caliper-0.1.2-darwin-arm64.tar.gz\n`, `${'0'.repeat(64)}  different.tar.gz\n`]) {
    const f = fixture(); f.put('caliper-0.1.2-darwin-arm64.tar.gz.sha256', checksum); assert.throws(() => f.validate());
  }
});
test('split manifest must match whole hash and each ordered part size/hash', () => {
  for (const modify of [m => m.sha256 = '0'.repeat(64), m => m.parts[0].bytes++, m => m.parts[0].sha256 = '0'.repeat(64), m => m.parts.reverse(), m => m.parts = []]) {
    const f = fixture(), name = 'caliper-0.1.2-linux-amd64.tar.gz.parts.json';
    const m = JSON.parse(f.metadata.get(name)); modify(m); f.put(name, JSON.stringify(m));
    assert.throws(() => f.validate());
  }
});
function registryFixture() {
  const pkg = {name: 'caliper-harness', version: '0.1.2'}, bytes = Buffer.from('packed npm fixture');
  const pack = {...pkg, integrity: `sha512-${sha(bytes, 'sha512', 'base64')}`};
  const existing = {...pkg, dist: {integrity: pack.integrity, tarball: 'https://registry.npmjs.org/caliper-harness/-/caliper-harness-0.1.2.tgz'}};
  const replies = new Map(), calls = [];
  const request = async url => {
    calls.push(url); const value = replies.get(url);
    if (value === undefined) return {status: 404, ok: false};
    if (typeof value === 'number') return {status: value, ok: false};
    return {status: 200, ok: true, json: async () => value, arrayBuffer: async () => value};
  };
  return {pkg, bytes, pack, existing, replies, calls, request, base: 'https://registry.npmjs.org/caliper-harness'};
}
test('absent npm version can publish, without confusing registry errors with absence', async () => {
  const f = registryFixture(); assert.equal((await registryState(f.pkg, f.pack, f.bytes, f.request)).publish, true);
  f.replies.set(`${f.base}/latest`, 503); await assert.rejects(registryState(f.pkg, f.pack, f.bytes, f.request), /503/);
});
test('identical existing publication is skipped only after its actual bytes are verified', async () => {
  const f = registryFixture(); f.replies.set(`${f.base}/latest`, f.existing); f.replies.set(f.existing.dist.tarball, f.bytes);
  assert.equal((await registryState(f.pkg, f.pack, f.bytes, f.request)).publish, false);
  assert.ok(f.calls.includes(f.existing.dist.tarball));
  f.replies.set(f.existing.dist.tarball, Buffer.from('different'));
  await assert.rejects(registryState(f.pkg, f.pack, f.bytes, f.request), /tarball bytes differ/);
});
test('different existing integrity and unexpected tarball origin refuse', async () => {
  const f = registryFixture(); f.replies.set(`${f.base}/latest`, f.existing);
  f.existing.dist.integrity = 'sha512-different'; await assert.rejects(registryState(f.pkg, f.pack, f.bytes, f.request), /different package integrity/);
  f.existing.dist.integrity = f.pack.integrity; f.existing.dist.tarball = 'https://other.example/test';
  await assert.rejects(registryState(f.pkg, f.pack, f.bytes, f.request), /unexpected origin/);
});
test('newer latest refuses an old event and does not fetch its tarball', async () => {
  const f = registryFixture(); f.replies.set(`${f.base}/latest`, {...f.existing, version: '0.2.0'});
  await assert.rejects(registryState(f.pkg, f.pack, f.bytes, f.request), /backwards/); assert.equal(f.calls.length, 1);
  assert.equal(newer('0.10.0', '0.9.0'), true); assert.equal(newer('0.1.2', '0.1.2'), false);
});
test('existing identical version with older or absent latest requires explicit reconciliation', async () => {
  for (const latest of [undefined, {version: '0.1.1'}]) {
    const f = registryFixture(); if (latest) f.replies.set(`${f.base}/latest`, latest);
    f.replies.set(`${f.base}/0.1.2`, f.existing); f.replies.set(f.existing.dist.tarball, f.bytes);
    await assert.rejects(registryState(f.pkg, f.pack, f.bytes, f.request), /reconcile the dist-tag/);
  }
});
test('local packed identity/integrity mismatch refuses before network access', async () => {
  const f = registryFixture(); f.pack.version = '0.1.3';
  await assert.rejects(registryState(f.pkg, f.pack, f.bytes, f.request), /identity/);
  f.pack.version = '0.1.2'; await assert.rejects(registryState(f.pkg, f.pack, Buffer.from('changed'), f.request), /pack integrity/);
  assert.equal(f.calls.length, 0);
});
