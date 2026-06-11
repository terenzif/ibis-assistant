package gitrepo

type ProviderName string

const (
	ProviderGitHub      ProviderName = "github"
	ProviderGitLab      ProviderName = "gitlab"
	ProviderAzureDevOps ProviderName = "azure_devops"
	ProviderGeneric     ProviderName = "generic"
)

type AuthType string

const (
	AuthTypeToken AuthType = "token"
	AuthTypeBasic AuthType = "basic"
	AuthTypeSSH   AuthType = "ssh"
)

type Credential struct {
	Target        string   `json:"target"` // Domain (e.g. github.com) or full repository URL
	Provider      string   `json:"provider"`
	AuthType      AuthType `json:"auth_type"`
	Token         string   `json:"token,omitempty"`
	Username      string   `json:"username,omitempty"`
	SSHPrivateKey string   `json:"ssh_private_key,omitempty"`
}

type CredentialsRequiredError struct {
	Provider string
	Target   string
	Message  string
}

func (e *CredentialsRequiredError) Error() string {
	return e.Message
}

