#!/usr/bin/env node

const childProcess = require('child_process');

const BINARY_MAP = {
    darwin_x64: {name: 'slack-mcp-server-darwin-amd64', suffix: ''},
    darwin_arm64: {name: 'slack-mcp-server-darwin-arm64', suffix: ''},
    linux_x64: {name: 'slack-mcp-server-linux-amd64', suffix: ''},
    linux_arm64: {name: 'slack-mcp-server-linux-arm64', suffix: ''},
    win32_x64: {name: 'slack-mcp-server-windows-amd64', suffix: '.exe'},
    win32_arm64: {name: 'slack-mcp-server-windows-arm64', suffix: '.exe'},
};

// Resolving will fail if the optionalDependency was not installed or the platform/arch is not supported
const resolveBinaryPath = () => {
    try {
        const binary = BINARY_MAP[`${process.platform}_${process.arch}`];
        return require.resolve(`${binary.name}/bin/${binary.name}${binary.suffix}`);
    } catch (e) {
        throw new Error(`Could not resolve binary path for platform/arch: ${process.platform}/${process.arch}`);
    }
};

childProcess.execFileSync(resolveBinaryPath(), process.argv.slice(2), {
    stdio: 'inherit',
});
