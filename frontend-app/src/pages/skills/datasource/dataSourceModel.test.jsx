import { describe, expect, it } from 'vitest';
import {
  datasourceDetailPagesWithDocument,
  normalizeDatasourceDocuments,
} from './dataSourceModel.js';

describe('datasource data model', () => {
  it('fails fast when the document-list contract has no documents array', () => {
    expect(() => normalizeDatasourceDocuments({})).toThrow(
      'datasourceV2/list response.documents must be an array',
    );
  });

  it('updates only the cached first detail-page document after a successful edit', () => {
    const current = {
      pages: [
        { document: { documentId: 5, fileName: 'old' }, chunks: [] },
        { document: null, chunks: [] },
      ],
    };
    const next = datasourceDetailPagesWithDocument(current, {
      documentId: 5,
      fileName: 'new',
    });
    expect(next.pages[0].document.fileName).toBe('new');
    expect(next.pages[1]).toBe(current.pages[1]);
  });
});
