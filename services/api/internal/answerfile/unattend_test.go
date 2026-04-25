package answerfile

import (
	"strings"
	"testing"
)

const minimalTpl = `<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend">
  <settings pass="oobeSystem">
    <component name="Microsoft-Windows-Shell-Setup" processorArchitecture="{{.Image.Arch}}" publicKeyToken="31bf3856ad364e35" language="neutral" versionScope="nonSxS">
      <ComputerName>{{upper .Machine.AssetTag}}</ComputerName>
      <TimeZone>{{.Image.Timezone}}</TimeZone>
      <UserAccounts>
        <LocalAccounts>
          <LocalAccount wcm:action="add">
            <Name>{{.LocalAdmin.Username}}</Name>
            <Password>
              <Value>{{accountPassword .LocalAdmin.PasswordPlain}}</Value>
              <PlainText>false</PlainText>
            </Password>
          </LocalAccount>
        </LocalAccounts>
      </UserAccounts>
{{- if .DomainJoin }}
      <Identification>
        <JoinDomain>{{.DomainJoin.Domain}}</JoinDomain>
        <Credentials>
          <Username>{{.DomainJoin.Username}}</Username>
          <Domain>{{.DomainJoin.Domain}}</Domain>
          <Password>{{.DomainJoin.Password}}</Password>
        </Credentials>
{{- if .DomainJoin.OU }}
        <MachineObjectOU>{{.DomainJoin.OU}}</MachineObjectOU>
{{- end }}
      </Identification>
{{- end }}
    </component>
  </settings>
</unattend>`

func TestRender_Basic(t *testing.T) {
	var in UnattendInput
	in.Machine.AssetTag = "lab-01"
	in.Image.Arch = "amd64"
	in.Image.Timezone = "UTC"
	in.LocalAdmin = LocalAdmin{Username: "Administrator", PasswordPlain: "Hunter2!"}

	out, err := Render(minimalTpl, in)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "<ComputerName>LAB-01</ComputerName>") {
		t.Errorf("expected uppercased ComputerName; got: %s", s)
	}
	if strings.Contains(s, "Hunter2!") {
		t.Errorf("plain password leaked into output: %s", s)
	}
	if !strings.Contains(s, "<Value>") {
		t.Errorf("expected encoded password value; got: %s", s)
	}
}

func TestRender_DomainJoinOptional(t *testing.T) {
	var in UnattendInput
	in.Machine.AssetTag = "x"
	in.Image.Arch = "amd64"
	in.LocalAdmin.PasswordPlain = "p"

	out, _ := Render(minimalTpl, in)
	if strings.Contains(string(out), "<JoinDomain>") {
		t.Error("domain join section should be absent when DomainJoin nil")
	}

	in.DomainJoin = &DomainJoin{Domain: "ACME.LOCAL", Username: "joiner", Password: "secret", OU: "OU=Workstations,DC=ACME,DC=LOCAL"}
	out, _ = Render(minimalTpl, in)
	if !strings.Contains(string(out), "<JoinDomain>ACME.LOCAL</JoinDomain>") {
		t.Error("expected JoinDomain in output")
	}
	if !strings.Contains(string(out), "<MachineObjectOU>OU=Workstations") {
		t.Error("expected MachineObjectOU")
	}
}

func TestRender_LocaleDefaults(t *testing.T) {
	var in UnattendInput
	in.Machine.AssetTag = "x"
	in.Image.Arch = "amd64"
	in.LocalAdmin.PasswordPlain = "p"
	out, _ := Render(minimalTpl, in)
	if strings.Contains(string(out), "<TimeZone></TimeZone>") {
		t.Error("expected timezone default to UTC")
	}
}

func TestRender_BadTemplateErrors(t *testing.T) {
	if _, err := Render("{{.NoSuchField.X}}", UnattendInput{}); err == nil {
		t.Error("expected error from bad template")
	}
}

func TestEncodeUTF16LE_RoundTrip(t *testing.T) {
	enc := encodeUTF16LE("ab")
	// a -> 0x61 0x00, b -> 0x62 0x00 → 4 bytes → "YQBiAA==" in base64
	if enc != "YQBiAA==" {
		t.Errorf("UTF-16LE base64 of 'ab' wrong: %q", enc)
	}
}
