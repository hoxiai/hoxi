package workcatalog

import (
	"context"
	"errors"
	"time"
)

func (s *Service) Publish(ctx context.Context, scope Scope, api Platform, id, revision string, authorize Authorize) (Receipt, error) {
	d, err := s.GetDraft(ctx, scope, id)
	if err != nil {
		return Receipt{}, err
	}
	if revision == "" || d.Revision != revision {
		return Receipt{}, errors.New("draft revision mismatch; read the diff again")
	}
	if d.Kind == "create" {
		return Receipt{}, errors.New("new-product publication is not supported by the configured platform adapter")
	}
	if d.Status == "published" && d.Receipt != nil {
		return *d.Receipt, nil
	}
	if d.Status == "publishing" {
		return s.Verify(ctx, scope, api, id)
	}
	if d.Status != "draft" {
		return Receipt{}, errors.New("draft is not publishable; use work_publish_verify for an uncertain previous attempt")
	}
	if authorize == nil {
		return Receipt{}, errors.New("desktop publication approval is unavailable")
	}
	if err := authorize(ctx, d); err != nil {
		return Receipt{}, err
	}
	unlock, err := s.store.lock(ctx, scope, ".operation.lock")
	if err != nil {
		return Receipt{}, err
	}
	defer unlock()
	// Another request may have published/discarded this immutable draft while the
	// desktop was showing its diff. Claim again under the cross-process lock.
	d, err = s.GetDraft(ctx, scope, id)
	if err != nil {
		return Receipt{}, err
	}
	if d.Status == "published" && d.Receipt != nil {
		return *d.Receipt, nil
	}
	if d.Status != "draft" || d.Revision != revision {
		return Receipt{}, errors.New("draft changed while waiting for approval")
	}
	live, err := api.Get(ctx, d.ProductID)
	if err != nil {
		return Receipt{}, err
	}
	if err := validateOwnedProduct(live, scope.SellerLoginID); err != nil {
		return Receipt{}, err
	}
	for _, change := range d.Changes {
		if !equalField(change.Field, fieldValue(live, change.Field), change.Before) {
			return s.setReceipt(ctx, scope, d, "conflict", "A changed field was updated online; sync and create a new draft", &live)
		}
	}
	// Persist BEFORE the external side effect. A crash or network timeout never
	// turns a retry into a second write: only read-only verification is allowed.
	if _, err := s.setReceipt(ctx, scope, d, "publishing", "Publication started; verify before any retry", nil); err != nil {
		return Receipt{}, err
	}
	if err := api.Update(ctx, live, d.Patch); err != nil {
		return s.setReceipt(context.WithoutCancel(ctx), scope, d, "unverified", "Platform write did not return a conclusive result; verify publication before retrying", nil)
	}
	return s.verify(ctx, scope, api, d)
}

func (s *Service) Verify(ctx context.Context, scope Scope, api Platform, id string) (Receipt, error) {
	unlock, err := s.store.lock(ctx, scope, ".operation.lock")
	if err != nil {
		return Receipt{}, err
	}
	defer unlock()
	d, err := s.GetDraft(ctx, scope, id)
	if err != nil {
		return Receipt{}, err
	}
	if d.Status != "publishing" && d.Status != "unverified" && d.Status != "published" {
		return Receipt{}, errors.New("draft has no publication attempt to verify")
	}
	return s.verify(ctx, scope, api, d)
}

func (s *Service) verify(ctx context.Context, scope Scope, api Platform, d Draft) (Receipt, error) {
	live, err := api.Get(ctx, d.ProductID)
	if err != nil {
		return s.setReceipt(context.WithoutCancel(ctx), scope, d, "unverified", "Readback is unavailable; use work_publish_verify later", nil)
	}
	if err := validateOwnedProduct(live, scope.SellerLoginID); err != nil {
		return Receipt{}, err
	}
	for _, change := range d.Changes {
		if !equalField(change.Field, fieldValue(live, change.Field), change.After) {
			return s.setReceipt(ctx, scope, d, "unverified", "Readback differs from the draft; the platform may be reviewing or normalizing it", &live)
		}
	}
	return s.setReceipt(ctx, scope, d, "published", "All changed fields matched the platform readback", &live)
}

func (s *Service) setReceipt(ctx context.Context, scope Scope, d Draft, status, message string, live *Product) (Receipt, error) {
	r := Receipt{DraftID: d.ID, ProductID: d.ProductID, Status: status, Message: message, CheckedAt: time.Now().UTC()}
	err := s.store.update(ctx, scope, func(st *state) error {
		current, ok := st.Drafts[d.ID]
		if !ok || current.Revision != d.Revision {
			return errors.New("publication draft revision changed")
		}
		current.Status, current.Receipt = status, &r
		st.Drafts[d.ID] = current
		if live != nil {
			st.Products[live.ID] = *live
			if st.Sync != nil {
				if _, exists := st.Sync.Products[live.ID]; exists {
					st.Sync.Products[live.ID] = *live
				}
			}
		}
		return nil
	})
	return r, err
}
