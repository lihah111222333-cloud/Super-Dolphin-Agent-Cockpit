import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";

installAppTestHooks();
const {
  it,
  createSharedFileState,
  mockSharedFileWorkflow,
  openSharedFilesPage,
  refreshSharedFilesFromBridge,
  refreshSharedFilesFromFocus,
  previewFinalSharedFile,
  exportAndDeleteWorkSharedFile,
  continueChatFromFinalSharedFile,
} = testEnv;

it('loads shared files from the shared-files RPC and wires open, export, delete, and continue actions', async () => {
  const sharedFiles = createSharedFileState();
  mockSharedFileWorkflow(sharedFiles);

  await openSharedFilesPage();
  await refreshSharedFilesFromBridge(sharedFiles);
  await refreshSharedFilesFromFocus(sharedFiles);
  await previewFinalSharedFile();
  await exportAndDeleteWorkSharedFile();
  await continueChatFromFinalSharedFile();
});
