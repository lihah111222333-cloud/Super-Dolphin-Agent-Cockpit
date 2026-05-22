#!/usr/bin/env node
'use strict';

const fs = require('fs');
const net = require('net');
const path = require('path');
const { spawnSync } = require('child_process');

const ROOT = path.resolve(__dirname, '..');
const DEFAULT_BASE_URL = process.env.PLAYWRIGHT_REAL_BASE_URL || 'http://127.0.0.1:4501';
const DEFAULT_SPECS = ['tests/e2e/chat-refresh.real.spec.js'];

function npmCmd() {
    return process.platform === 'win32' ? 'npm.cmd' : 'npm';
}

function playwrightBin() {
    const name = process.platform === 'win32' ? 'playwright.cmd' : 'playwright';
    return path.resolve(ROOT, 'node_modules', '.bin', name);
}

function parseArgs(argv) {
    const args = {
        headed: false,
        skipBuild: false,
        baseURL: DEFAULT_BASE_URL,
        specArgs: [],
    };

    for (const raw of argv) {
        const value = (raw || '').toString().trim();
        if (!value) continue;
        if (value === '--headed') {
            args.headed = true;
            continue;
        }
        if (value === '--skip-build') {
            args.skipBuild = true;
            continue;
        }
        if (value.startsWith('--base-url=')) {
            args.baseURL = value.slice('--base-url='.length).trim() || DEFAULT_BASE_URL;
            continue;
        }
        if (value === '--help' || value === '-h') {
            args.help = true;
            continue;
        }
        args.specArgs.push(value);
    }

    return args;
}

function printHelp() {
    console.log(`\n一键真实环境回归\n\n用法:\n  node scripts/run-real-e2e.cjs [--headed] [--skip-build] [--base-url=http://127.0.0.1:4501] [spec ...]\n\n示例:\n  npm run test:e2e:real\n  npm run test:e2e:real -- --headed\n  npm run test:e2e:real -- tests/e2e/chat-refresh.real.spec.js\n  npm run test:e2e:real -- --base-url=http://127.0.0.1:4502\n`);
}

function runCommand(command, commandArgs, extraEnv = {}) {
    const result = spawnSync(command, commandArgs, {
        cwd: ROOT,
        stdio: 'inherit',
        shell: process.platform === 'win32',
        windowsHide: true,
        env: {
            ...process.env,
            ...extraEnv,
        },
    });
    if (result.error) {
        console.error(`Failed to run ${command}: ${result.error.message}`);
        return 1;
    }
    if (result.signal) {
        console.error(`${command} exited by signal ${result.signal}`);
        return 1;
    }
    if (typeof result.status === 'number') {
        return result.status;
    }
    console.error(`${command} exited without a status code`);
    return 1;
}

function checkTcpReachable(baseURL) {
    let parsed;
    try {
        parsed = new URL(baseURL);
    } catch (error) {
        throw new Error(`无效 base URL: ${baseURL} (${error.message})`);
    }

    const port = Number(parsed.port || (parsed.protocol === 'https:' ? 443 : 80));
    const host = parsed.hostname;

    return new Promise((resolve, reject) => {
        const socket = net.createConnection({ host, port });
        const timer = setTimeout(() => {
            socket.destroy();
            reject(new Error(`无法连接 ${host}:${port}`));
        }, 3000);
        socket.once('connect', () => {
            clearTimeout(timer);
            socket.end();
            resolve();
        });
        socket.once('error', (error) => {
            clearTimeout(timer);
            reject(error);
        });
    });
}

async function main() {
    const options = parseArgs(process.argv.slice(2));
    if (options.help) {
        printHelp();
        return 0;
    }

    const specs = options.specArgs.length > 0 ? options.specArgs : DEFAULT_SPECS;
    const playwright = playwrightBin();

    if (!fs.existsSync(playwright)) {
        console.error('❌ 未找到本地 Playwright 可执行文件，请先执行 npm install');
        return 1;
    }

    console.log(`🔎 真实环境地址: ${options.baseURL}`);
    console.log(`🧪 执行用例: ${specs.join(', ')}`);

    try {
        await checkTcpReachable(options.baseURL);
    } catch (error) {
        console.error(`\n❌ 真实调试环境不可达: ${error.message}`);
        console.error('💡 请先在仓库根目录启动:');
        console.error('   make run-agent-terminal-debug');
        return 1;
    }

    if (!options.skipBuild) {
        console.log('\n🏗️ 先构建前端 dist ...');
        const buildCode = runCommand(npmCmd(), ['run', 'build']);
        if (buildCode !== 0) return buildCode;
    }

    console.log('\n🚀 开始执行真实环境 E2E ...');
    const playwrightArgs = ['test', '-c', 'playwright.real.config.js'];
    if (options.headed) {
        playwrightArgs.push('--headed');
    }
    playwrightArgs.push(...specs);

    return runCommand(playwright, playwrightArgs, {
        PLAYWRIGHT_REAL_BASE_URL: options.baseURL,
    });
}

main()
    .then((code) => process.exit(code))
    .catch((error) => {
        console.error('\n❌ 一键真实环境回归失败:', error);
        process.exit(1);
    });
