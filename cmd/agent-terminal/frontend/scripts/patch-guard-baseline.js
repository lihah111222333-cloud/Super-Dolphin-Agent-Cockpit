const fs = require('fs');
const sizeGuardPath = 'cmd/agent-terminal/frontend/scripts/size-guard.cjs';
let code = fs.readFileSync(sizeGuardPath, 'utf8');

const replacement = `
        // ── 坏范式检查 ──
        const badParadigms = detectBadParadigms(lines, rel);
        for (const bp of badParadigms) {
            const key = bp.file + ':' + bp.line;
            if (baseline.paradigms && baseline.paradigms.includes(key)) {
                continue; // 存量不追溯
            }
            if (!newBaseline.paradigms) newBaseline.paradigms = [];
            newBaseline.paradigms.push(key);
            
            violations.push({
                type: 'paradigm',
                file: bp.file,
                message: \`\${bp.file}:\${bp.line} \${bp.message}\`,
            });
        }
        
        // 保留旧基线，避免丢失
        if (baseline.paradigms) {
            if (!newBaseline.paradigms) newBaseline.paradigms = [];
            for (const oldKey of baseline.paradigms) {
                if (oldKey.startsWith(rel + ':') && !newBaseline.paradigms.includes(oldKey)) {
                    // 只保留还存在的
                    const lineStr = oldKey.split(':')[1];
                    if (parseInt(lineStr) <= lines.length) {
                        newBaseline.paradigms.push(oldKey);
                    }
                }
            }
        }
`;

code = code.replace(/        \/\/ ── 坏范式检查 ──[\s\S]*?        \}/, replacement.trim());
fs.writeFileSync(sizeGuardPath, code);
