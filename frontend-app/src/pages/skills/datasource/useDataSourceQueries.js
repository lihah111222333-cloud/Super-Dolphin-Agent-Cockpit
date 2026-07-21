import { useEffect, useMemo } from 'react';
import { useInfiniteQuery, useQuery } from '@tanstack/react-query';
import { runBackgroundAction } from '../../../shared/ui/runUIAction.js';
import {
  combineDatasourceDetailPages,
  datasourceDocumentQueryKey,
  datasourceDocumentsQueryKey,
  fetchDatasourceDetailPage,
  normalizeDatasourceDocuments,
  DATASOURCE_LIST_LIMIT,
} from './dataSourceModel.js';
import { skillsPageService } from '../services/skillsPageService.js';

export function useDataSourceQueries(detailID) {
  const documentsQuery = useQuery({
    queryKey: datasourceDocumentsQueryKey(),
    queryFn: () =>
      runBackgroundAction('datasource.documents.load', async () =>
        normalizeDatasourceDocuments(
          await skillsPageService.listDatasourceDocuments({
            limit: DATASOURCE_LIST_LIMIT,
          }),
        ),
      ),
  });
  const detailQuery = useInfiniteQuery({
    queryKey: datasourceDocumentQueryKey(detailID),
    enabled: detailID > 0,
    initialPageParam: null,
    queryFn: ({ pageParam }) =>
      runBackgroundAction('datasource.detail.load', () =>
        fetchDatasourceDetailPage(detailID, pageParam),
      ),
    getNextPageParam: (lastPage) =>
      lastPage.hasMore ? lastPage.nextCursor : undefined,
  });
  const {
    fetchNextPage,
    hasNextPage,
    isError: detailError,
    isFetchingNextPage,
  } = detailQuery;
  useEffect(() => {
    if (
      detailID <= 0 ||
      detailError ||
      !hasNextPage ||
      isFetchingNextPage
    )
      return;
    void fetchNextPage();
  }, [
    detailID,
    detailError,
    fetchNextPage,
    hasNextPage,
    isFetchingNextPage,
  ]);
  const { data: documents = [] } = documentsQuery;
  return {
    documents,
    documentsQuery,
    detailData: useMemo(
      () => combineDatasourceDetailPages(detailQuery.data?.pages),
      [detailQuery.data],
    ),
    detailQuery,
  };
}
