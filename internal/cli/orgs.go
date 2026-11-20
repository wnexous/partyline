package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/wnexous/partyline/internal/api/dto"
	"github.com/wnexous/partyline/internal/cliconfig"
)

// loginOrgsTimeout bounds the request behind the line `partyline login` prints about the default organization:
// the line is a courtesy and never holds a login up.
const loginOrgsTimeout = 5 * time.Second

// orgsCmd runs `partyline orgs [slug]` (ORG-04): the organizations of the token's user - address, name and
// permission - with a star on the one a new AI joins when --org is not given; with an address, only that one.
// The default itself is a setting of the account, changed in the web app: agent tokens only reach the agent API
// (AD-020). Organizations have no chart any more (SR2-05).
func orgsCmd(ctx context.Context, args []string, io stdio) int {
	fs := flag.NewFlagSet("orgs", flag.ContinueOnError)
	fs.SetOutput(io.stderr)
	url := fs.String("url", "", "Partyline base URL (default $PARTYLINE_URL)")
	token := fs.String("token", "", "agent token (default $PARTYLINE_TOKEN)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fmt.Fprintln(io.stderr, "partyline orgs: takes at most one organization")
		return 2
	}
	creds, err := cliconfig.Resolve(cliconfig.Config{URL: *url, Token: *token}, io.getenv)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline orgs:", err)
		return 1
	}
	if creds.URL == "" || creds.Token == "" {
		fmt.Fprintln(io.stderr, "partyline orgs: set PARTYLINE_URL and PARTYLINE_TOKEN (or --url and --token), or run partyline setup")
		return 2
	}
	list, err := fetchAgentOrgs(ctx, creds)
	if err != nil {
		fmt.Fprintln(io.stderr, "partyline orgs:", err)
		return 1
	}
	ref := strings.TrimSpace(fs.Arg(0))
	if ref == "" {
		printOrgs(io.stdout, list)
		return 0
	}
	org, ok := findOrg(list, ref)
	if !ok {
		fmt.Fprintf(io.stderr, "partyline orgs: you belong to no organization with the address %q; partyline orgs lists yours\n", ref)
		return 1
	}
	printOrgs(io.stdout, []dto.AgentOrg{org})
	return 0
}

// fetchAgentOrgs reads the organizations of the token's user from GET /api/v1/agent/orgs.
func fetchAgentOrgs(ctx context.Context, creds cliconfig.Config) ([]dto.AgentOrg, error) {
	list, err := getFromAgentAPI[dto.List[dto.AgentOrg]](ctx, creds, "orgs")
	return list.Items, err
}

// findOrg finds the organization ref names among the user's: its address in any case, or its id.
func findOrg(list []dto.AgentOrg, ref string) (dto.AgentOrg, bool) {
	for _, o := range list {
		if strings.EqualFold(o.Slug, ref) || o.ID == ref {
			return o, true
		}
	}
	return dto.AgentOrg{}, false
}

// printOrgs writes the organizations in tab-aligned columns under a header - address (starred when it is the
// default), name and permission - and then how the default is used and changed. A user in none is told how to
// get one.
func printOrgs(out io.Writer, list []dto.AgentOrg) {
	if len(list) == 0 {
		fmt.Fprintln(out, "You belong to no organization: create one in the web app, or ask an admin to invite you.")
		return
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  SLUG\tNAME\tPERMISSION")
	for _, o := range list {
		marker := "  "
		if o.Default {
			marker = "* "
		}
		fmt.Fprintln(w, strings.Join([]string{marker + o.Slug, o.Name, o.Permission}, "\t"))
	}
	_ = w.Flush()
	fmt.Fprintln(out)
	fmt.Fprintln(out, "New AIs join the one marked * unless you pass --org; change your default in Settings.")
}

// printDefaultOrg tells which organization the new AIs of the user just logged in join - the one GET /agent/orgs
// marks as the default - in the words "New AIs join <name> (<address>); pass --org or change your default in
// Settings." It says nothing when it cannot learn it (the call fails, or no organization is marked), so a
// login never fails or waits long for it.
func printDefaultOrg(ctx context.Context, cfg cliconfig.Config, io stdio) {
	ctx, cancel := context.WithTimeout(ctx, loginOrgsTimeout)
	defer cancel()
	list, err := fetchAgentOrgs(ctx, cfg)
	if err != nil {
		return
	}
	for _, o := range list {
		if o.Default {
			fmt.Fprintf(io.stdout, "New AIs join %s (%s); pass --org or change your default in Settings.\n", o.Name, o.Slug)
			return
		}
	}
}
