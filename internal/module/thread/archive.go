package thread

import "context"

func (s *service) Archive(ctx context.Context, threadID string) error {
	if err := s.updateThreadStatus(ctx, threadID, statusArchived); err != nil {
		return err
	}
	if err := s.setBindingArchived(ctx, threadID, true); err != nil {
		return err
	}
	if err := s.closeSessionIfActive(ctx, threadID); err != nil {
		return err
	}
	s.publishThreadStopped(threadID, s.lookupThreadAgent(threadID), statusArchived, "archived")
	return nil
}

func (s *service) Unarchive(ctx context.Context, threadID string) error {
	if err := s.updateThreadStatus(ctx, threadID, statusCreated); err != nil {
		return err
	}
	return s.setBindingArchived(ctx, threadID, false)
}
