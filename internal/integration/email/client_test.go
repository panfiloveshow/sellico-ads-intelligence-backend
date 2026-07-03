package email

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeRecipients(t *testing.T) {
	recipients, err := normalizeRecipients([]string{" owner@example.com ", "", "client@example.com"})

	require.NoError(t, err)
	assert.Equal(t, []string{"owner@example.com", "client@example.com"}, recipients)
}

func TestNormalizeRecipientsRejectsInvalidAddress(t *testing.T) {
	_, err := normalizeRecipients([]string{"not-an-email"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid email recipient")
}

func TestBuildPlainTextMessageSanitizesSubjectHeader(t *testing.T) {
	message := buildPlainTextMessage("Sellico <reports@example.com>", []string{"client@example.com"}, "Client\nAudit", "real report body")

	assert.Contains(t, message, "From: Sellico <reports@example.com>")
	assert.Contains(t, message, "To: client@example.com")
	assert.Contains(t, message, "Subject: Client Audit")
	assert.Contains(t, message, "Content-Type: text/plain; charset=UTF-8")
	assert.Contains(t, message, "\r\n\r\nreal report body")
}
