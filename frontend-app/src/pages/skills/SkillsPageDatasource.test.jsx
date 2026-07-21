import { describe, expect, it } from 'vitest';
import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { frontendHealthSnapshot } from '../../shared/diagnostics/frontendHealthStore.js';
import { backend, deferred, renderSkillsPage } from './SkillsPageTestSupport.jsx';

describe('SkillsPage backend migration: datasource', () => {
  it('renders datasource_v2 rows and sends create, read, update, and delete actions', async () => {
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: /数据源|Data Sources/ }));

    expect(await screen.findByText('source.txt')).toBeInTheDocument();
    expect(backend.listDatasourceDocuments).toHaveBeenCalledWith({ limit: 200 });

    fireEvent.click(screen.getByTestId('datasource-import-button'));
    await waitFor(() => {
      expect(backend.selectDatasourceImportFile).toHaveBeenCalledWith({
        filters: [{ displayName: 'PDF/TXT/TEXT', pattern: '*.pdf;*.txt;*.text' }],
      });
      expect(backend.selectFiles).not.toHaveBeenCalled();
      expect(backend.importDatasourceLocalFile).toHaveBeenCalledWith({
        sourcePath: 'C:\\data\\new.pdf',
        pickerToken: 'picker-token',
      });
    });

    fireEvent.click(screen.getByTestId('datasource-view-101'));
    await waitFor(() => {
      expect(backend.getDatasourceDocument).toHaveBeenCalledWith({ documentId: 101 });
      expect(backend.listDatasourceChunks).toHaveBeenCalledWith({ documentId: 101, limit: 50, cursor: 0 });
    });
    const detailDialog = await screen.findByRole('dialog', { name: '数据源详情' });
    const chunks = await within(detailDialog).findAllByTestId('datasource-detail-chunk');
    expect(chunks.map((chunk) => chunk.textContent)).toEqual(['content', 'more content']);
    fireEvent.click(within(detailDialog).getByRole('button', { name: '关闭' }));

    fireEvent.click(screen.getByTestId('datasource-edit-101'));
    const editDialog = await screen.findByRole('dialog', { name: '编辑数据源' });
    fireEvent.change(within(editDialog).getByTestId('datasource-edit-source-path'), {
      target: { value: 'C:\\data\\source-renamed.txt' },
    });
    fireEvent.change(within(editDialog).getByTestId('datasource-edit-file-name'), {
      target: { value: 'source-renamed.txt' },
    });
    fireEvent.click(within(editDialog).getByTestId('datasource-edit-save'));
    await waitFor(() => {
      expect(backend.updateDatasourceDocument).toHaveBeenCalledWith(expect.objectContaining({
        documentId: 101,
        sourcePath: 'C:\\data\\source-renamed.txt',
        fileName: 'source-renamed.txt',
      }));
    });

    fireEvent.click(screen.getByTestId('datasource-delete-101'));
    const deleteDialog = await screen.findByRole('dialog', { name: '删除数据源' });
    fireEvent.click(within(deleteDialog).getByTestId('datasource-delete-confirm'));
    await waitFor(() => {
      expect(backend.deleteDatasourceDocument).toHaveBeenCalledWith({ documentId: 101 });
    });
  });

  it('does not publish datasource import success when the contract layer rejects the response', async () => {
    backend.importDatasourceLocalFile.mockRejectedValueOnce(new TypeError('datasource import response rejected by contract'));
    renderSkillsPage();
    fireEvent.click(screen.getByRole('button', { name: /数据源|Data Sources/ }));
    expect(await screen.findByText('source.txt')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('datasource-import-button'));
    await waitFor(() => expect(backend.importDatasourceLocalFile).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.queryByText('已导入数据源。')).not.toBeInTheDocument());
  });

  it('does not clear datasource delete state when the contract layer rejects the response', async () => {
    backend.deleteDatasourceDocument.mockRejectedValueOnce(new TypeError('datasource delete response rejected by contract'));
    renderSkillsPage();
    fireEvent.click(screen.getByRole('button', { name: /数据源|Data Sources/ }));
    expect(await screen.findByText('source.txt')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('datasource-delete-101'));
    const dialog = await screen.findByRole('dialog', { name: '删除数据源' });
    fireEvent.click(within(dialog).getByTestId('datasource-delete-confirm'));
    await waitFor(() => expect(backend.deleteDatasourceDocument).toHaveBeenCalledWith({ documentId: 101 }));
    expect(screen.queryByText('已删除数据源。')).not.toBeInTheDocument();
    expect(screen.getByRole('dialog', { name: '删除数据源' })).toBeInTheDocument();
  });

  it('renders the first datasource chunk before later chunk pages finish and appends the next page', async () => {
    const nextPage = deferred();
    backend.listDatasourceChunks.mockImplementationOnce(() => nextPage.promise);
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: /数据源|Data Sources/ }));
    expect(await screen.findByText('source.txt')).toBeInTheDocument();

    fireEvent.click(screen.getByTestId('datasource-view-101'));

    const detailDialog = await screen.findByRole('dialog', { name: '数据源详情' });
    const firstChunk = await within(detailDialog).findByTestId('datasource-detail-chunk');
    expect(firstChunk).toHaveTextContent('content');
    expect(within(detailDialog).queryByText('more content')).not.toBeInTheDocument();
    await waitFor(() => {
      expect(backend.listDatasourceChunks).toHaveBeenCalledWith({ documentId: 101, limit: 50, cursor: 0 });
    });

    nextPage.resolve({
      chunks: [{
        id: 502,
        documentId: 101,
        chunkIndex: 1,
        content: 'more content',
        charCount: 12,
        byteCount: 12,
      }],
      hasMore: false,
      nextCursor: 1,
    });

    await waitFor(() => {
      const chunks = within(detailDialog).getAllByTestId('datasource-detail-chunk');
      expect(chunks.map((chunk) => chunk.textContent)).toEqual(['content', 'more content']);
    });
  });

  it('fails fast when a datasource list response documents field is malformed', async () => {
    backend.listDatasourceDocuments.mockResolvedValueOnce({ documents: {} });
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: /数据源|Data Sources/ }));

    expect(await screen.findByRole('alert')).toHaveTextContent('操作失败：读取数据源失败，请重试。');
    expect(screen.queryByText(/response\.documents/)).not.toBeInTheDocument();
    expect(frontendHealthSnapshot()).toEqual(expect.arrayContaining([
      expect.objectContaining({ actionId: 'datasource.documents.load' }),
    ]));
    expect(backend.listDatasourceDocuments).toHaveBeenCalledWith({ limit: 200 });
  });

  it('fails fast when a datasource chunk page reports hasMore without chunks', async () => {
    backend.getDatasourceDocument.mockResolvedValueOnce({
      document: {
        documentId: 101,
        sourcePath: 'C:\\data\\source.txt',
        fileName: 'source.txt',
        extension: '.txt',
        sizeBytes: 7,
        chunkCount: 1,
        totalChars: 7,
        status: 'ready',
      },
      chunks: [],
      hasMore: true,
      nextCursor: 0,
    });
    renderSkillsPage();

    fireEvent.click(screen.getByRole('button', { name: /数据源|Data Sources/ }));
    expect(await screen.findByText('source.txt')).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('datasource-view-101'));

    const detailDialog = await screen.findByRole('dialog', { name: '数据源详情' });
    expect(await within(detailDialog).findByRole('alert')).toHaveTextContent('操作失败：读取详情失败，请重试。');
    expect(within(detailDialog).queryByText(/hasMore without chunks/)).not.toBeInTheDocument();
    expect(backend.listDatasourceChunks).not.toHaveBeenCalled();
  });
});
