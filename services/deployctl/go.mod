module github.com/your-org/deployserver/deployctl

go 1.25.12

require github.com/your-org/deployserver/sdk v0.0.0

// The SDK lives in the same repo; dogfood it from source so a spec/SDK
// change that breaks the machines commands is caught at build time.
replace github.com/your-org/deployserver/sdk => ../sdk
