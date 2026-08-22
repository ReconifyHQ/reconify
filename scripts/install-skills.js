#!/usr/bin/env node
'use strict';

const fs = require('fs');
const path = require('path');

const PKG_ROOT = path.resolve(__dirname, '..');
const TARGET = process.cwd();

if (TARGET === PKG_ROOT) {
  console.log('Skills are already present in this directory (running from the Reconify repo itself).');
  process.exit(0);
}

const SKILL_DIRS = [
  { source: 'skills/.agents', target: '.agents/skills' },
  { source: 'skills/.claude', target: '.claude/skills' },
  { source: 'skills/.codex', target: '.codex/skills' },
];

function copyDir(src, dst) {
  fs.mkdirSync(dst, { recursive: true });
  for (const entry of fs.readdirSync(src, { withFileTypes: true })) {
    const srcPath = path.join(src, entry.name);
    const dstPath = path.join(dst, entry.name);
    if (entry.isDirectory()) {
      copyDir(srcPath, dstPath);
    } else {
      fs.copyFileSync(srcPath, dstPath);
      console.log(`  wrote ${path.relative(TARGET, dstPath)}`);
    }
  }
}

console.log('Installing Reconify agent skills...\n');

let installed = 0;
for (const dir of SKILL_DIRS) {
  const src = path.join(PKG_ROOT, dir.source);
  if (!fs.existsSync(src)) continue;
  const dst = path.join(TARGET, dir.target);
  copyDir(src, dst);
  installed++;
}

if (installed === 0) {
  console.error('Error: skill source files not found in package. Re-install the package and try again.');
  process.exit(1);
}

console.log(`\nDone. Skills installed into ${SKILL_DIRS.map((dir) => dir.target).join(', ')}.`);
console.log('Run `go run ./cmd/reconify config schema` to verify the CLI is working.');
