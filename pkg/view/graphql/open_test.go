package graphql

import (
	"context"
	"io/ioutil"
	"os"
	"testing"

	"mash/db"
	"mash/pkg/analytics/disabled"
	"mash/pkg/auth"
	service_auth "mash/pkg/auth/service"
	"mash/pkg/codebase"
	db_codebase "mash/pkg/codebase/db"
	service_codebase "mash/pkg/codebase/service"
	"mash/pkg/graphql/resolvers"
	"mash/pkg/internal/sturdytest"
	db_snapshots "mash/pkg/snapshots/db"
	"mash/pkg/snapshots/snapshotter"
	"mash/pkg/user"
	db_user "mash/pkg/user/db"
	"mash/pkg/view"
	db_view "mash/pkg/view/db"
	"mash/pkg/view/events"
	"mash/pkg/view/view_workspace_snapshot"
	"mash/pkg/workspace"
	db_workspace "mash/pkg/workspace/db"
	service_workspace "mash/pkg/workspace/service"
	db_workspace_watchers "mash/pkg/workspace/watchers/db"
	service_workspace_watchers "mash/pkg/workspace/watchers/service"
	"mash/vcs"
	"mash/vcs/executor"
	"mash/vcs/provider"

	"github.com/google/uuid"
	"github.com/graph-gophers/graphql-go"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

func newRepoProvider(t *testing.T) provider.RepoProvider {
	reposBasePath, err := ioutil.TempDir(os.TempDir(), "mash")
	assert.NoError(t, err)
	return provider.New(reposBasePath, "localhost:8888")
}

func TestUpdateViewWorkspace(t *testing.T) {
	if os.Getenv("E2E_TEST") == "" {
		t.SkipNow()
	}

	repoProvider := newRepoProvider(t)

	d, err := db.Setup(
		sturdytest.PsqlDbSourceForTesting(),
		true,
		"file://../../../db/migrations",
	)
	if err != nil {
		panic(err)
	}

	viewRepo := db_view.NewRepo(d)
	userRepo := db_user.NewRepo(d)
	codebaseRepo := db_codebase.NewRepo(d)
	codebaseUserRepo := db_codebase.NewCodebaseUserRepo(d)
	snapshotRepo := db_snapshots.NewRepo(d)
	workspaceRepo := db_workspace.NewRepo(d)
	logger, _ := zap.NewDevelopment()
	executorProvider := executor.NewProvider(logger, repoProvider)
	codebaseViewEvents := events.NewInMemory()
	eventsSender := events.NewSender(codebaseUserRepo, workspaceRepo, codebaseViewEvents)
	gitSnapshotter := snapshotter.NewGitSnapshotter(snapshotRepo, workspaceRepo, workspaceRepo, viewRepo, eventsSender, executorProvider, logger)
	postHogClient := disabled.NewClient()

	workspaceWatcherRepo := db_workspace_watchers.NewInMemory()
	workspaceWatchersService := service_workspace_watchers.New(workspaceWatcherRepo, eventsSender)

	viewWorkspaceSnapshotsRepo := view_workspace_snapshot.NewRepo(d)

	workspaceService := service_workspace.New(
		zap.NewNop(),
		postHogClient,
		workspaceRepo,
		workspaceRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		executorProvider,
		eventsSender,
		nil,
		gitSnapshotter,
		nil,
	)

	codebaseService := service_codebase.New(codebaseRepo, codebaseUserRepo)
	authService := service_auth.New(codebaseService, nil, workspaceService, nil /*aclProvider*/, nil /*organizationService*/)

	userID := uuid.New()
	err = userRepo.Create(&user.User{ID: userID.String(), Email: userID.String() + "@test.com"})
	assert.NoError(t, err)

	viewResolver := NewResolver(
		viewRepo,
		workspaceRepo,
		gitSnapshotter,
		viewWorkspaceSnapshotsRepo,
		snapshotRepo,
		nil,
		nil,
		workspaceRepo,
		codebaseViewEvents,
		eventsSender,
		executorProvider,
		logger,
		nil,
		workspaceWatchersService,
		postHogClient,
		nil,
		authService,
	)

	authCtx := auth.NewContext(context.Background(), &auth.Subject{Type: auth.SubjectUser, ID: userID.String()})

	type steps struct {
		workspace       string
		expected        string
		toWrite         string
		toWriteNewFile  string
		expectInNewFile string
	}

	cases := []struct {
		name  string
		steps []steps
	}{
		{
			name: "navigate-between-two-workspaces",
			steps: []steps{
				{workspace: "A", expected: "hello world\n", toWrite: "AA"},
				{workspace: "B", expected: "hello world\n", toWrite: "BB"},
				{workspace: "A", expected: "AA", toWrite: "AAaa"},
				{workspace: "B", expected: "BB", toWrite: "BBbb"},
				{workspace: "A", expected: "AAaa"},
				{workspace: "B", expected: "BBbb"},
				{workspace: "A", expected: "AAaa"},
				{workspace: "B", expected: "BBbb", toWriteNewFile: "stuff"},
				{workspace: "A", expected: "AAaa"},
				{workspace: "B", expected: "BBbb", expectInNewFile: "stuff"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			codebaseID := uuid.NewString()
			viewID := uuid.NewString()

			trunkPath := repoProvider.TrunkPath(codebaseID)
			viewPath := repoProvider.ViewPath(codebaseID, viewID)

			workspaceAID := uuid.New()
			workspaceBID := uuid.New()

			err := codebaseRepo.Create(codebase.Codebase{
				ID:              codebaseID,
				ShortCodebaseID: codebase.ShortCodebaseID(codebaseID), // not realistic
			})
			assert.NoError(t, err)
			assert.NoError(t, codebaseUserRepo.Create(codebase.CodebaseUser{
				ID:         uuid.NewString(),
				CodebaseID: codebaseID,
				UserID:     userID.String(),
			}))
			assert.NoError(t, workspaceRepo.Create(workspace.Workspace{
				ID:         workspaceAID.String(),
				CodebaseID: codebaseID,
				UserID:     userID.String(),
			}))
			assert.NoError(t, workspaceRepo.Create(workspace.Workspace{
				ID:         workspaceBID.String(),
				CodebaseID: codebaseID,
				UserID:     userID.String(),
			}))
			err = viewRepo.Create(view.View{
				ID:          viewID,
				UserID:      userID.String(),
				CodebaseID:  codebaseID,
				WorkspaceID: workspaceAID.String(),
			})
			assert.NoError(t, err)

			_, err = vcs.CreateBareRepoWithRootCommit(trunkPath)
			if err != nil {
				panic(err)
			}
			repoA, err := vcs.CloneRepo(trunkPath, viewPath)
			if err != nil {
				panic(err)
			}

			// Create common history
			assert.NoError(t, repoA.CheckoutBranchWithForce("sturdytrunk"))
			assert.NoError(t, ioutil.WriteFile(viewPath+"/a.txt", []byte("hello world\n"), 0666))
			_, err = repoA.AddAndCommit("Commit 1 (in A)")
			assert.NoError(t, err)
			assert.NoError(t, repoA.Push(zap.NewNop(), "sturdytrunk"))

			trunkLogEntries, err := repoA.LogBranch("sturdytrunk", 10)
			assert.NoError(t, err)

			// Create two branches
			assert.NoError(t, repoA.CreateNewBranchOnHEAD(workspaceAID.String()))
			assert.NoError(t, repoA.CreateNewBranchOnHEAD(workspaceBID.String()))

			assert.NoError(t, repoA.Push(zap.NewNop(), workspaceAID.String()))
			assert.NoError(t, repoA.Push(zap.NewNop(), workspaceBID.String()))

			for _, s := range tc.steps {
				var workspaceID string
				if s.workspace == "A" {
					workspaceID = workspaceAID.String()
				} else if s.workspace == "B" {
					workspaceID = workspaceBID.String()
				}

				_, err = viewResolver.OpenWorkspaceOnView(authCtx, resolvers.OpenViewArgs{
					Input: resolvers.OpenWorkspaceOnViewInput{
						WorkspaceID: graphql.ID(workspaceID),
						ViewID:      graphql.ID(viewID),
					},
				})
				assert.NoError(t, err)

				// Content as expected
				fileContent, err := ioutil.ReadFile(viewPath + "/a.txt")
				assert.NoError(t, err)
				assert.Equal(t, s.expected, string(fileContent))
				// New file content as expected
				if s.expectInNewFile != "" {
					fileContent, err := ioutil.ReadFile(viewPath + "/newfile.txt")
					assert.NoError(t, err)
					assert.Equal(t, s.expectInNewFile, string(fileContent))
				} else {
					// File not expected to be there
					assert.NoFileExists(t, viewPath+"/newfile.txt")
				}
				// No new commits
				wsLogEntries, err := repoA.LogBranch(workspaceID, 10)
				assert.NoError(t, err)
				assert.Equal(t, trunkLogEntries, wsLogEntries)
				// Write some new unsaved changes
				if s.toWrite != "" {
					assert.NoError(t, ioutil.WriteFile(viewPath+"/a.txt", []byte(s.toWrite), 0666))
				}
				if s.toWriteNewFile != "" {
					assert.NoError(t, ioutil.WriteFile(viewPath+"/newfile.txt", []byte(s.toWriteNewFile), 0666))
				}
			}

		})
	}
}
