// demo-setup prepares a demonstration stand (ADR-033): workstations
// РМ-01…РМ-N, one instructor and one trainee per workstation, spread over
// the three exercises — ДДС района, ДДС скорой (03) and оператор 112. It
// goes through auth.Service like the admin screens do, and is idempotent:
// an existing workstation or login is left as it is (a password is never
// changed), so it is safe to run again after adding workstations.
//
//	DEMO_PASSWORD=… emsim demo-setup --actor admin
//
// DEMO_PASSWORD (required, 8+ characters) is every new account's
// password; DEMO_WORKSTATIONS (default 6) is N.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"emsim/internal/auth"
	authpg "emsim/internal/auth/postgres"
	"emsim/internal/content"
	contentpg "emsim/internal/content/postgres"

	"github.com/jackc/pgx/v5"
)

var (
	errDemoUsage        = errors.New("usage: emsim demo-setup --actor <admin login>")
	errDemoPassword     = errors.New("DEMO_PASSWORD must be set (8 or more characters)")
	errDemoWorkstations = errors.New("DEMO_WORKSTATIONS must be a number from 1 to 99")
)

const defaultDemoWorkstations = 6

// demoTrack is one exercise a demo trainee practises.
type demoTrack struct {
	serviceCode *string
	label       string
}

func demoTracks() []demoTrack {
	district, ambulance := "dds_district_chertanovo", "dds_ambulance_03"
	return []demoTrack{
		{serviceCode: &district, label: "ДДС района"},
		{serviceCode: &ambulance, label: "ДДС 03"},
		{label: "оператор 112"},
	}
}

func runDemoSetup(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("demo-setup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	actorLogin := flags.String("actor", "", "active admin login")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *actorLogin == "" {
		return errDemoUsage
	}
	password := os.Getenv("DEMO_PASSWORD")
	if len(password) < 8 {
		return errDemoPassword
	}
	count := defaultDemoWorkstations
	if raw := os.Getenv("DEMO_WORKSTATIONS"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 99 {
			return errDemoWorkstations
		}
		count = parsed
	}
	pool, err := openReadyDatabase(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	actorID, actorRole, err := resolveImportActor(ctx, pool, *actorLogin)
	if err != nil {
		return err
	}
	actor := auth.Principal{UserID: actorID, Role: auth.Role(actorRole)}
	authStore := authpg.NewStore(pool)
	contentService := content.NewService(contentpg.NewStore(pool), mustSchemaValidator())
	// Demo accounts are for a class demonstration: their passwords are
	// shared knowledge, so the login policy's "must change" flag is left off.
	demoPolicy := auth.DefaultPolicy()
	demoPolicy.ForceChangeRoles = nil
	authService := auth.NewService(authStore, auth.NewPasswordIdentityProvider(authStore), 0, nil, contentService).WithPolicy(demoPolicy)

	// Workstations: keep every active one, add the missing РМ-NN.
	existing, err := authService.ListWorkstations(ctx)
	if err != nil {
		return err
	}
	byNumber := map[int]auth.Workstation{}
	var keep []auth.Workstation
	for _, w := range existing {
		byNumber[w.Number] = w
		if w.Active {
			keep = append(keep, w)
		}
	}
	added := 0
	for number := 1; number <= count; number++ {
		if w, ok := byNumber[number]; ok && w.Active {
			continue
		} else if ok {
			keep = append(keep, w)
		} else {
			keep = append(keep, auth.Workstation{Number: number, Label: fmt.Sprintf("РМ-%02d", number)})
		}
		added++
	}
	if added > 0 {
		if _, err := authService.ReplaceWorkstations(ctx, keep, actor, "demo-setup"); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "workstations: %d ready, %d added or reactivated\n", count, added)

	type demoUser struct {
		login, fullName string
		role            auth.Role
		track           demoTrack
	}
	users := []demoUser{{login: "instructor", fullName: "Преподаватель (демо)", role: auth.RoleInstructor}}
	tracks := demoTracks()
	for number := 1; number <= count; number++ {
		track := tracks[(number-1)%len(tracks)]
		users = append(users, demoUser{
			login: fmt.Sprintf("trainee%02d", number), fullName: fmt.Sprintf("Обучаемый %02d (%s)", number, track.label),
			role: auth.RoleTrainee, track: track,
		})
	}
	for _, u := range users {
		exists, err := loginExists(ctx, authStore, u.login)
		if err != nil {
			return err
		}
		state := "exists, left unchanged"
		if !exists {
			if _, err := authService.CreateUser(ctx, auth.NewUser{
				Login: u.login, Password: password, FullName: u.fullName, Role: u.role, ServiceCode: u.track.serviceCode,
			}, actor, "demo-setup"); err != nil {
				return err
			}
			state = "created"
		}
		exercise := ""
		if u.role == auth.RoleTrainee {
			exercise = " · " + u.track.label
		}
		fmt.Fprintf(out, "%-10s %-10s%s: %s\n", u.login, u.role, exercise, state)
	}
	return nil
}

func loginExists(ctx context.Context, store *authpg.Store, login string) (bool, error) {
	var found bool
	err := store.WithTx(ctx, func(tx pgx.Tx) error {
		_, err := store.UserByLogin(ctx, tx, login)
		if errors.Is(err, auth.ErrNotFound) {
			return nil
		}
		found = err == nil
		return err
	})
	return found, err
}
