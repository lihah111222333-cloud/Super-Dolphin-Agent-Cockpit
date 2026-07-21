export function consumerValidatedRegression() {
  return `
    import { vi } from 'vitest'
    import { loadConfig } from './consumer.js'
    vi.mock('../../shared/api/backendApi.js', () => ({
      readConfig: vi.fn().mockResolvedValue({ malformed: true }),
    }))
    it('rejects malformed config', async () => {
      await expect(loadConfig()).rejects.toThrow('invalid config')
    })
  `;
}
