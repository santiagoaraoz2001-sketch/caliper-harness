'use strict';
// Publication checks only. The installed npm launcher does not ship this file.
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const platforms = ['darwin-arm64', 'windows-amd64', 'linux-amd64', 'linux-arm64'];
const repository = 'santiagoaraoz2001-sketch/caliper-harness';
const stable = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;
function requireThat(condition, message) { if (!condition) throw new Error(message); }
function validateSource(release, head, tagHead) {
  requireThat(/^[a-f0-9]{40}$/.test(head), 'Checked-out source must be an exact commit');
  requireThat(release.target_commitish === head, 'Release target differs from the checked-out build source');
  requireThat(release.draft || tagHead === head, 'Published tag differs from the checked-out build source');
  return {source: head, draft: release.draft, tag: release.draft ? null : tagHead};
}
function digest(bytes, algorithm = 'sha256', encoding = 'hex') { return crypto.createHash(algorithm).update(bytes).digest(encoding); }
function validateRelease(tag, pkg, lock, release, readMetadata, dryRun) {
  const version = tag.startsWith('v') ? tag.slice(1) : '';
  requireThat(stable.test(version), 'Release tag must be stable vX.Y.Z');
  requireThat(pkg.name === 'caliper-harness' && pkg.version === version, 'Release tag and npm package identity differ');
  requireThat(lock.name === pkg.name && lock.version === version && lock.packages?.['']?.name === pkg.name && lock.packages[''].version === version, 'Package lock identity differs');
  requireThat(release.tag_name === tag && !release.prerelease && (dryRun || !release.draft), 'Release must match the stable tag; only dry runs accept drafts');
  const releaseURL = `https://github.com/${repository}/releases/tag/`;
  requireThat(release.html_url === `${releaseURL}${tag}` || (dryRun && release.draft && release.html_url?.startsWith(`${releaseURL}untagged-`)), 'Unexpected GitHub release repository');
  const assets = new Map();
  for (const asset of release.assets || []) {
    requireThat(!assets.has(asset.name), `Duplicate release asset: ${asset.name}`);
    assets.set(asset.name, asset);
  }
  function asset(name) {
    const entry = assets.get(name);
    requireThat(entry?.state === 'uploaded' && Number.isSafeInteger(entry.size) && entry.size > 0, `Missing or incomplete release asset: ${name}`);
    requireThat(/^sha256:[a-f0-9]{64}$/.test(entry.digest || ''), `GitHub SHA256 digest missing: ${name}`);
    return entry;
  }
  function metadata(name) {
    const entry = asset(name), bytes = readMetadata(name);
    requireThat(bytes.length === entry.size && `sha256:${digest(bytes)}` === entry.digest, `Downloaded metadata differs from GitHub asset: ${name}`);
    return bytes.toString('utf8');
  }
  const transports = {};
  for (const platform of platforms) {
    const archive = `caliper-${version}-${platform}.tar.gz`;
    const checksum = metadata(`${archive}.sha256`).trim();
    const fields = checksum.split(/\s+/);
    requireThat(fields.length === 2 && /^[a-f0-9]{64}$/.test(fields[0]) && fields[1] === archive, `Invalid archive checksum: ${platform}`);
    if (assets.has(archive)) {
      requireThat(asset(archive).digest === `sha256:${fields[0]}`, `Archive checksum differs from GitHub digest: ${platform}`);
      transports[platform] = archive;
    } else {
      const manifestName = `${archive}.parts.json`, manifest = JSON.parse(metadata(manifestName));
      requireThat(manifest.format === 1 && manifest.archive === archive && manifest.sha256 === fields[0] && Array.isArray(manifest.parts) && manifest.parts.length, `Invalid split archive: ${platform}`);
      for (const [index, part] of manifest.parts.entries()) {
        const name = `${archive}.part-${String(index).padStart(3, '0')}`;
        requireThat(part.name === name, `Unexpected archive part name: ${platform}`);
        const entry = asset(name);
        requireThat(part.bytes === entry.size && /^[a-f0-9]{64}$/.test(part.sha256 || '') && entry.digest === `sha256:${part.sha256}`, `Archive part differs from GitHub digest/size: ${name}`);
      }
      transports[platform] = manifestName;
    }
  }
  return { name: pkg.name, version, tag, dryRun, transports };
}
function newer(left, right) {
  requireThat(stable.test(left) && stable.test(right), 'Registry latest must be a stable version');
  const a = left.split('.').map(BigInt), b = right.split('.').map(BigInt);
  for (let i = 0; i < a.length; i++) { if (a[i] !== b[i]) return a[i] > b[i]; }
  return false;
}
async function registryState(pkg, pack, bytes, request = fetch) {
  requireThat(pack.name === pkg.name && pack.version === pkg.version, 'Packed npm identity differs');
  const integrity = `sha512-${digest(bytes, 'sha512', 'base64')}`;
  requireThat(pack.integrity === integrity, 'Packed tarball does not match npm pack integrity');
  const endpoint = `https://registry.npmjs.org/${encodeURIComponent(pkg.name)}`;
  async function read(url) {
    const response = await request(url);
    if (response.status === 404) return null;
    requireThat(response.ok, `npm registry request failed: HTTP ${response.status}`);
    return response.json();
  }
  const latest = await read(`${endpoint}/latest`);
  requireThat(!latest || !newer(latest.version, pkg.version), `Refusing to move npm latest backwards from ${latest?.version} to ${pkg.version}`);
  const existing = latest?.version === pkg.version ? latest : await read(`${endpoint}/${pkg.version}`);
  if (!existing) return { state: 'absent', publish: true, integrity };
  requireThat(existing.name === pkg.name && existing.version === pkg.version && existing.dist?.integrity === integrity, 'Existing npm version has different package integrity; refusing to skip or overwrite');
  const tarball = new URL(existing.dist.tarball);
  requireThat(tarball.origin === 'https://registry.npmjs.org', 'Existing npm tarball has an unexpected origin');
  const response = await request(tarball.href);
  requireThat(response.ok, `Existing npm tarball download failed: HTTP ${response.status}`);
  requireThat(`sha512-${digest(Buffer.from(await response.arrayBuffer()), 'sha512', 'base64')}` === integrity, 'Existing npm tarball bytes differ from the candidate');
  requireThat(latest?.version === pkg.version, 'Existing package is identical but npm latest is not this release; maintainer must reconcile the dist-tag');
  return { state: 'identical', publish: false, integrity };
}
async function main() {
  const [command, ...args] = process.argv.slice(2);
  const pkg = JSON.parse(fs.readFileSync('scripts/npm/package.json'));
  if (command === 'source') {
    const [releaseFile, head, tagHead] = args;
    console.log(JSON.stringify(validateSource(JSON.parse(fs.readFileSync(releaseFile)), head, tagHead), null, 2));
  } else if (command === 'release') {
    const [tag, releaseFile, metadataDirectory, mode] = args;
    requireThat(mode === 'dry-run' || mode === 'publish', 'Expected dry-run or publish mode');
    const result = validateRelease(tag, pkg, JSON.parse(fs.readFileSync('scripts/npm/package-lock.json')), JSON.parse(fs.readFileSync(releaseFile)), name => fs.readFileSync(path.join(metadataDirectory, name)), mode === 'dry-run');
    console.log(JSON.stringify(result, null, 2));
  } else if (command === 'registry') {
    const [packFile] = args, packed = JSON.parse(fs.readFileSync(packFile));
    requireThat(packed.length === 1 && path.basename(packed[0].filename) === packed[0].filename, 'Expected one local npm tarball');
    const file = path.resolve(path.dirname(packFile), packed[0].filename);
    const result = await registryState(pkg, packed[0], fs.readFileSync(file));
    if (process.env.GITHUB_OUTPUT) fs.appendFileSync(process.env.GITHUB_OUTPUT, `publish=${result.publish}\npackage_path=${file}\n`);
    console.log(JSON.stringify(result, null, 2));
  } else throw new Error('Usage: check-npm-release.cjs source RELEASE_JSON HEAD TAG_HEAD | release TAG RELEASE_JSON METADATA_DIR MODE | registry PACK_JSON');
}
if (require.main === module) main().catch(error => { console.error(`npm publication check failed: ${error.message}`); process.exitCode = 1; });
module.exports = { validateRelease, validateSource, registryState, newer, platforms };
