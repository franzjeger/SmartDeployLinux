package sdk_test

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	sdk "github.com/your-org/deployserver/sdk"
)

// Construct a client and list machines. Examples without an `// Output:`
// line are compiled (so they can't rot) but not executed.
func Example() {
	c, err := sdk.New(sdk.Options{
		BaseURL: os.Getenv("DEPLOY_API_URL"),
		Token:   os.Getenv("DEPLOY_API_TOKEN"),
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	machines, err := c.ListMachines(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, m := range machines {
		tag := "(untagged)"
		if m.AssetTag != nil {
			tag = *m.AssetTag
		}
		fmt.Printf("%s\t%s\n", m.ID, tag)
	}
}

// Typed error classification: distinguish "not found" from "forbidden"
// from transport errors without string-matching.
func ExampleIsNotFound() {
	c, _ := sdk.New(sdk.Options{BaseURL: "https://deploy.example.com", Token: "tok"})

	_, err := c.GetMachine(context.Background(), "does-not-exist")
	switch {
	case err == nil:
		fmt.Println("found")
	case sdk.IsNotFound(err):
		fmt.Println("no such machine")
	case sdk.IsForbidden(err):
		fmt.Println("missing RBAC permission")
	default:
		var apiErr *sdk.APIError
		if errors.As(err, &apiErr) {
			fmt.Printf("api error %d: %s\n", apiErr.Status, apiErr.Message)
		} else {
			fmt.Printf("transport error: %v\n", err)
		}
	}
}

// Issue a one-shot deployment code for a machine+profile, the core
// operator action behind `deployctl deploy`.
func ExampleClient_IssueDeployment() {
	c, _ := sdk.New(sdk.Options{BaseURL: "https://deploy.example.com", Token: "tok"})

	res, err := c.IssueDeployment(context.Background(), sdk.IssueDeploymentInput{
		MachineID:  "1f4c…",
		ProfileID:  "9ab2…",
		TTLSeconds: 3600,
		IssuedFor:  "bench-rebuild",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("boot code %s valid until %s\n", res.Code, res.ExpiresAt.Format(time.RFC3339))
}
