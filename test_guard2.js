const fs = require('fs');

function detectBadParadigms(lines, rel) {
    const violations = [];
    for (let i = 0; i < lines.length; i++) {
        let line = lines[i];
        let trimmed = line.trim();
        if (trimmed.startsWith('//') || trimmed.startsWith('/*') || trimmed.startsWith('*')) continue;
        
        let noStr = line.replace(/(["'`]).*?(?<!\\)\1/g, '""');
        let noOpt = noStr.replace(/\?\./g, '');
        let noNullish = noOpt.replace(/\?\?/g, '');
        
        let qCount = (noNullish.match(/\?/g) || []).length;
        let cCount = (noNullish.match(/:/g) || []).length;
        if (qCount >= 2 && cCount >= 2) {
            // Also ensure it is actually a nested ternary by simple regex
            if (/\?.*?\?.*?:/.test(noNullish) || /\?.*?:.*?\?.*?:/.test(noNullish)) {
                violations.push({
                    file: rel, line: i + 1, message: `嵌套三元表达式`
                });
            }
        }
        
        if (/\.\.\.\s*\(.*?\?.*?\{.*?\}.*?:\s*\{\}\s*\)/.test(noStr)) {
            violations.push({
                file: rel, line: i + 1, message: `内联条件对象展开`
            });
        }
        
        if (/\.cache\.delete\(.*?\.cache\.keys\(\)\.next\(\)\.value\)/.test(noStr)) {
            violations.push({
                file: rel, line: i + 1, message: `伪LRU缓存`
            });
        }
    }
    return violations;
}

function walk(dir) {
    let results = [];
    const list = fs.readdirSync(dir);
    list.forEach(function(file) {
        file = dir + '/' + file;
        const stat = fs.statSync(file);
        if (stat && stat.isDirectory() && !file.includes('node_modules')) { 
            results = results.concat(walk(file));
        } else if (file.endsWith('.js')) {
            const content = fs.readFileSync(file, 'utf8');
            const lines = content.split('\n');
            results = results.concat(detectBadParadigms(lines, file));
        }
    });
    return results;
}

const v = walk('./cmd/agent-terminal/frontend/vue-app');
console.log(v);
