import { installAppTestHooks, testEnv } from "./test-utils/appTestHarness.jsx";

installAppTestHooks();
const {
  act,
  fireEvent,
  render,
  screen,
  within,
  expect,
  it,
  vi,
  App,
  backend,
} = testEnv;

it('shows an app update banner after the background check finds a new version', async () => {
    vi.useFakeTimers();
    backend.checkAppUpdate.mockResolvedValueOnce({ enabled: true, available: true, version: '0.1.1' });

    render(<App />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });

    const banner = screen.getByTestId('app-update-banner');
    expect(banner).toHaveTextContent('发现新版本 0.1.1');
    expect(banner).toHaveTextContent('建议更新到最新版');
    expect(backend.checkAppUpdate).toHaveBeenCalledTimes(1);
  });

it('shows a dismissible safe notice when the background update check rejects', async () => {
    vi.useFakeTimers();
    backend.checkAppUpdate.mockRejectedValueOnce(new Error('release endpoint returned a private backend detail'));

    render(<App />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });

    const failure = screen.getByTestId('app-update-check-failure');
    expect(failure).toHaveTextContent('更新检查暂时不可用');
    expect(failure).toHaveTextContent('更新检查暂时不可用，请稍后再试。');
    expect(failure).not.toHaveTextContent('private backend detail');

    fireEvent.click(screen.getByRole('button', { name: '切换到 English' }));
    expect(failure).toHaveTextContent('Update check is temporarily unavailable');
    expect(failure).toHaveTextContent('We could not check for updates. Please try again later.');
    expect(failure).not.toHaveTextContent('private backend detail');

    fireEvent.click(within(failure).getByRole('button', { name: 'Close' }));
    expect(screen.queryByTestId('app-update-check-failure')).not.toBeInTheDocument();
  });

it('shows a fixed recovery banner when the background update signature check fails', async () => {
    vi.useFakeTimers();
    const secret = 'codesign output /Applications/Super Dolphin.app';
    const failure = new Error(secret);
    failure.data = {
      code: 'UPDATE_SIGNATURE_INVALID',
      retryable: false,
      action: 'preserve_state_export_diagnostics',
      transaction_id: '',
    };
    backend.checkAppUpdate.mockRejectedValueOnce(failure);

    render(<App />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });

    const banner = screen.getByTestId('app-update-banner');
    expect(banner).toHaveTextContent('更新完整性校验失败，请保持现场并导出诊断信息。');
    expect(banner).not.toHaveTextContent(secret);
    expect(screen.queryByRole('button', { name: '立即更新' })).not.toBeInTheDocument();
  });

it('starts installing the latest update from the main update banner', async () => {
    vi.useFakeTimers();
    backend.checkAppUpdate.mockResolvedValueOnce({ enabled: true, available: true, version: '0.1.1' });
    backend.installLatestAppUpdate.mockResolvedValueOnce({ started: true, helper: 'updater' });

    render(<App />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '立即更新' }));
      await Promise.resolve();
    });
    expect(backend.installLatestAppUpdate).toHaveBeenCalledTimes(1);
    expect(screen.getByText('安装程序已启动，请按提示完成更新。')).toBeInTheDocument();
  });

it('redacts typed integrity details when update installation fails', async () => {
    vi.useFakeTimers();
    backend.checkAppUpdate.mockResolvedValueOnce({ enabled: true, available: true, version: '0.1.1' });
    const secret = 'codesign output /Applications/Super Dolphin.app';
    const failure = new Error(secret);
    failure.data = {
      code: 'UPDATE_SIGNATURE_INVALID',
      retryable: false,
      action: 'preserve_state_export_diagnostics',
      transaction_id: '',
    };
    backend.installLatestAppUpdate.mockRejectedValueOnce(failure);

    render(<App />);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2100);
    });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: '立即更新' }));
      await Promise.resolve();
    });

    const banner = screen.getByTestId('app-update-banner');
    expect(banner).toHaveTextContent('更新完整性校验失败，请保持现场并导出诊断信息。');
    expect(banner).not.toHaveTextContent(secret);
  });
