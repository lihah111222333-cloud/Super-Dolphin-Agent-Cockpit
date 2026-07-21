import { useCallback, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { runUIAction } from '../../../shared/ui/runUIAction.js';
import { cleanScalar } from '../../shared/pageShared.js';
import { skillsPageService } from '../services/skillsPageService.js';
import {
  DATASOURCE_IMPORT_FILTERS,
  DATASOURCE_UI,
  datasourceDetailPagesWithDocument,
  datasourceDocumentQueryKey,
  datasourceDocumentsQueryKey,
  normalizeDatasourceDocument,
} from './dataSourceModel.js';

export async function importDatasourceSelection(ctx) {
  await ctx.facade.importDatasourceLocalFile({
    sourcePath: ctx.sourcePath,
    pickerToken: ctx.pickerToken,
  });
  ctx.setSourcePath('');
  ctx.setNotice(ctx.successText);
  await ctx.invalidateDocuments();
}

export function useDataSourceActions(detailID, editingDoc, deletingDoc) {
  const queryClient = useQueryClient();
  const [busyAction, setBusyAction] = useState('');
  const [notice, setNotice] = useState('');
  const [actionError, setActionError] = useState('');
  const invalidateDocuments = useCallback(async () => {
    await queryClient.invalidateQueries({
      queryKey: datasourceDocumentsQueryKey(),
    });
  }, [queryClient]);
  const handleImport = useCallback(
    (setSourcePath) =>
      runUIAction('datasource.import', async () => {
        setBusyAction('import');
        setNotice('');
        setActionError('');
        try {
          const selected = await skillsPageService.selectDatasourceImportFile({
            filters: DATASOURCE_IMPORT_FILTERS,
          });
          const selectedPath = cleanScalar(selected?.sourcePath);
          if (!selectedPath) return;
          const pickerToken = cleanScalar(selected?.pickerToken);
          if (!pickerToken) throw new Error('pickerToken is required');
          setSourcePath(selectedPath);
          await importDatasourceSelection({
            facade: skillsPageService,
            invalidateDocuments,
            pickerToken,
            setNotice,
            setSourcePath,
            sourcePath: selectedPath,
            successText: DATASOURCE_UI.importSuccess,
          });
        } catch (error) {
          setActionError(`${DATASOURCE_UI.errorPrefix}请重试。`);
          throw error;
        } finally {
          setBusyAction('');
        }
      }),
    [invalidateDocuments],
  );
  const handleUpdate = useCallback(
    (form, closeEdit) =>
      runUIAction('datasource.update', async () => {
        if (!editingDoc) return;
        setBusyAction('update');
        setNotice('');
        setActionError('');
        try {
          const updated = await skillsPageService.updateDatasourceDocument({
            documentId: editingDoc.documentId,
            sourcePath: form.sourcePath,
            fileName: form.fileName,
            extension: form.extension,
            sizeBytes: form.sizeBytes,
          });
          closeEdit();
          const normalized = normalizeDatasourceDocument(updated, 0);
          if (detailID === normalized.documentId)
            queryClient.setQueryData(
              datasourceDocumentQueryKey(detailID),
              (current) =>
                datasourceDetailPagesWithDocument(current, normalized),
            );
          setNotice(DATASOURCE_UI.updateSuccess);
          await invalidateDocuments();
        } catch (error) {
          setActionError(`${DATASOURCE_UI.errorPrefix}请重试。`);
          throw error;
        } finally {
          setBusyAction('');
        }
      }),
    [detailID, editingDoc, invalidateDocuments, queryClient],
  );
  const handleDelete = useCallback(
    (closeDelete, closeDetail) =>
      runUIAction('datasource.delete', async () => {
        if (!deletingDoc) return;
        const documentID = deletingDoc.documentId;
        setBusyAction('delete');
        setNotice('');
        setActionError('');
        try {
          await skillsPageService.deleteDatasourceDocument({
            documentId: documentID,
          });
          closeDelete();
          if (detailID === documentID) closeDetail();
          queryClient.removeQueries({
            queryKey: datasourceDocumentQueryKey(documentID),
          });
          setNotice(DATASOURCE_UI.deleteSuccess);
          await invalidateDocuments();
        } catch (error) {
          setActionError(`${DATASOURCE_UI.errorPrefix}请重试。`);
          throw error;
        } finally {
          setBusyAction('');
        }
      }),
    [deletingDoc, detailID, invalidateDocuments, queryClient],
  );
  return {
    actionError,
    busyAction,
    handleDelete,
    handleImport,
    handleUpdate,
    notice,
  };
}
