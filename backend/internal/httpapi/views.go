package httpapi

import (
	"context"
	"errors"
	"reading-tracker/backend/internal/domain"
)

type userView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type deviceView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// installationView is the "installation" object of spec §2.3/§2.5.
type installationView struct {
	ID            string  `json:"id"`
	WorkspaceName string  `json:"workspace_name"`
	Status        string  `json:"status"`
	SetupError    string  `json:"setup_error,omitempty"`
	LibraryURL    *string `json:"library_url"`
}

func viewInstallation(i *domain.Installation) *installationView {
	if i == nil {
		return nil
	}
	v := &installationView{ID: i.ID, WorkspaceName: i.WorkspaceName, Status: i.Status, SetupError: i.SetupError}
	if i.LibraryURL != "" {
		u := i.LibraryURL
		v.LibraryURL = &u
	}
	return v
}

// activeInstallation loads the principal's active installation, or nil if none.
func (h *handlers) activeInstallation(ctx context.Context, p *Principal) (*domain.Installation, error) {
	id := p.InstallationID()
	if id == "" {
		return nil, nil
	}
	inst, err := h.Store.GetInstallation(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	return inst, err
}
