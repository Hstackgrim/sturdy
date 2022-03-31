package db

import (
	"fmt"

	"getsturdy.com/api/pkg/github"

	"github.com/jmoiron/sqlx"
)

type GitHubInstallationRepository interface {
	GetByID(id string) (*github.Installation, error)
	GetByOwner(owner string) (*github.Installation, error)
	GetByInstallationID(int64) (*github.Installation, error)
	Create(github.Installation) error
	Update(*github.Installation) error
}

type gitHubInstallationRepository struct {
	db *sqlx.DB
}

func NewGitHubInstallationRepository(db *sqlx.DB) GitHubInstallationRepository {
	return &gitHubInstallationRepository{db}
}

func (r *gitHubInstallationRepository) GetByID(id string) (*github.Installation, error) {
	var res github.Installation
	err := r.db.Get(&res, "SELECT * FROM github_installations WHERE id=$1 AND uninstalled_at IS NULL", id)
	if err != nil {
		return nil, fmt.Errorf("failed to query table: %w", err)
	}
	return &res, nil
}

func (r *gitHubInstallationRepository) GetByOwner(owner string) (*github.Installation, error) {
	var res github.Installation
	err := r.db.Get(&res, "SELECT * FROM github_installations WHERE owner=$1 AND uninstalled_at IS NULL", owner)
	if err != nil {
		return nil, fmt.Errorf("failed to query table: %w", err)
	}
	return &res, nil
}

func (r *gitHubInstallationRepository) GetByInstallationID(installationID int64) (*github.Installation, error) {
	var res github.Installation
	err := r.db.Get(&res, "SELECT * FROM github_installations WHERE installation_id=$1 AND uninstalled_at IS NULL", installationID)
	if err != nil {
		return nil, fmt.Errorf("failed to query table: %w", err)
	}
	return &res, nil
}

func (r *gitHubInstallationRepository) Create(i github.Installation) error {
	_, err := r.db.NamedExec(`INSERT INTO github_installations (id, installation_id, owner, created_at)
		VALUES (:id, :installation_id, :owner, :created_at)`, &i)
	if err != nil {
		return fmt.Errorf("failed to perform insert: %w", err)
	}
	return nil
}

func (r *gitHubInstallationRepository) Update(i *github.Installation) error {
	_, err := r.db.NamedExec(`UPDATE github_installations
			SET uninstalled_at = :uninstalled_at,
			    has_workflows_permission = :has_workflows_permission
			WHERE installation_id=:installation_id`, i)
	if err != nil {
		return fmt.Errorf("failed to update repo: %w", err)
	}
	return nil
}
