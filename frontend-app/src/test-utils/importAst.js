import ts from 'typescript';

function importSpecifierText(node) {
  return ts.isStringLiteral(node) ? node.text : '';
}

function namedImportEntry(element, specifier) {
  return {
    kind: 'named',
    imported: (element.propertyName || element.name).text,
    local: element.name.text,
    specifier,
  };
}

export function parseStaticImports(source, fileName = 'source.tsx') {
  const sourceFile = ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const imports = [];

  for (const statement of sourceFile.statements) {
    if (!ts.isImportDeclaration(statement)) continue;
    const specifier = importSpecifierText(statement.moduleSpecifier);
    if (!specifier || !statement.importClause) continue;

    if (statement.importClause.name) {
      imports.push({
        kind: 'default',
        imported: 'default',
        local: statement.importClause.name.text,
        specifier,
      });
    }

    const { namedBindings } = statement.importClause;
    if (!namedBindings) continue;
    if (ts.isNamespaceImport(namedBindings)) {
      imports.push({
        kind: 'namespace',
        imported: '*',
        local: namedBindings.name.text,
        specifier,
      });
      continue;
    }
    for (const element of namedBindings.elements) {
      imports.push(namedImportEntry(element, specifier));
    }
  }

  return imports;
}
