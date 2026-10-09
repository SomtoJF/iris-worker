package jobapplication

import (
	"fmt"
	"regexp"
	"strings"

	jobapplicationprofile "github.com/SomtoJF/iris-worker/workflow/jobapplication/profile"
)

// PhoneFormat represents different phone number format styles
type PhoneFormat string

const (
	PhoneFormatUS            PhoneFormat = "us"            // (XXX) XXX-XXXX
	PhoneFormatInternational PhoneFormat = "international" // +XX XXXX...
	PhoneFormatDots          PhoneFormat = "dots"          // XXX.XXX.XXXX
	PhoneFormatDashes        PhoneFormat = "dashes"        // XXX-XXX-XXXX
	PhoneFormatSpaces        PhoneFormat = "spaces"        // XXX XXX XXXX
	PhoneFormatPlain         PhoneFormat = "plain"         // XXXXXXXXXX (no formatting)
)

// FieldFormatter handles deterministic formatting of structured profile data
type FieldFormatter struct {
	phoneFormat PhoneFormat
	countryType string // "code" for "US", "name" for "United States"
}

// NewFieldFormatter creates a new formatter. Format detection happens lazily
// by analyzing the first field of each type.
func NewFieldFormatter() *FieldFormatter {
	return &FieldFormatter{
		phoneFormat: PhoneFormatPlain, // default
		countryType: "name",           // default
	}
}

// FormatValue formats a profile field value according to detected format.
// profileFieldName should be one of: Email, Phone, CountryOfResidence, FirstName, LastName, LinkedInUrl
func (f *FieldFormatter) FormatValue(profileFieldName string, value string, userProfile jobapplicationprofile.UserProfile) string {
	switch profileFieldName {
	case "Email":
		return value // Email is standardized, no formatting needed

	case "Phone":
		return f.formatPhoneNumber(value)

	case "CountryOfResidence":
		return f.formatCountry(value)

	case "FirstName", "LastName", "LinkedInUrl":
		return value // These are typically not formatted

	default:
		return value
	}
}

// DetectPhoneFormatFromPlaceholder analyzes a field's placeholder/label to infer phone format
func (f *FieldFormatter) DetectPhoneFormatFromPlaceholder(label, placeholder, description string) {
	// Only detect once; if already set to something other than plain, skip
	if f.phoneFormat != PhoneFormatPlain {
		return
	}

	combined := strings.ToLower(label + " " + placeholder + " " + description)

	// US format indicators
	if strings.Contains(combined, "(") && strings.Contains(combined, ")") {
		f.phoneFormat = PhoneFormatUS
		return
	}

	// International format with +
	if strings.Contains(combined, "+") {
		f.phoneFormat = PhoneFormatInternational
		return
	}

	// Dots format
	if strings.Count(combined, ".") >= 2 && !strings.Contains(combined, "-") {
		f.phoneFormat = PhoneFormatDots
		return
	}

	// Dashes format (but not US format which has parens)
	if strings.Count(combined, "-") >= 2 && !strings.Contains(combined, "(") {
		f.phoneFormat = PhoneFormatDashes
		return
	}

	// Spaces format
	if strings.Count(combined, " ") >= 2 && !strings.Contains(combined, "(") {
		f.phoneFormat = PhoneFormatSpaces
		return
	}

	// Default to plain
	f.phoneFormat = PhoneFormatPlain
}

// DetectCountryFormatFromField analyzes a field's options/placeholder to infer country format
func (f *FieldFormatter) DetectCountryFormatFromField(label, placeholder, description string) {
	// If country type already detected, don't override
	if f.countryType != "name" {
		return
	}

	combined := strings.ToLower(label + " " + placeholder + " " + description)

	// If we see country codes like "US", "UK", "CA", etc., it's code format
	if strings.Contains(combined, "code") || strings.Contains(combined, "abbreviation") {
		f.countryType = "code"
		return
	}

	// If we see "country" and example full names, it's name format
	if strings.Contains(combined, "country") && !strings.Contains(combined, "code") {
		f.countryType = "name"
		return
	}
}

// formatPhoneNumber formats a phone number according to detected format
func (f *FieldFormatter) formatPhoneNumber(phone string) string {
	// Remove all non-digit characters
	digits := regexp.MustCompile(`\D`).ReplaceAllString(phone, "")

	// Handle international format (starts with +)
	if strings.HasPrefix(phone, "+") {
		return "+" + digits
	}

	// Handle different formats based on phone length and detected format
	switch f.phoneFormat {
	case PhoneFormatUS:
		if len(digits) == 10 {
			return fmt.Sprintf("(%s) %s-%s", digits[:3], digits[3:6], digits[6:])
		}
		return digits

	case PhoneFormatInternational:
		// Keep as is or normalize with +
		if len(digits) > 10 {
			return "+" + digits
		}
		return digits

	case PhoneFormatDots:
		if len(digits) == 10 {
			return fmt.Sprintf("%s.%s.%s", digits[:3], digits[3:6], digits[6:])
		}
		return digits

	case PhoneFormatDashes:
		if len(digits) == 10 {
			return fmt.Sprintf("%s-%s-%s", digits[:3], digits[3:6], digits[6:])
		}
		return digits

	case PhoneFormatSpaces:
		if len(digits) == 10 {
			return fmt.Sprintf("%s %s %s", digits[:3], digits[3:6], digits[6:])
		}
		return digits

	case PhoneFormatPlain:
		fallthrough
	default:
		return digits
	}
}

// formatCountry formats a country value according to detected format
func (f *FieldFormatter) formatCountry(country string) string {
	if country == "" {
		return ""
	}

	switch f.countryType {
	case "code":
		// Convert full country name to code if needed
		return countryNameToCode(country)

	case "name":
		// Keep full country name
		return country

	default:
		return country
	}
}

// countryNameToCode converts country name to ISO 2-letter code
func countryNameToCode(countryName string) string {
	// If it's already a code (2 letters), return as is
	if len(countryName) == 2 {
		return strings.ToUpper(countryName)
	}

	// Map common country names to codes
	codeMap := map[string]string{
		"united states":        "US",
		"usa":                  "US",
		"united kingdom":       "GB",
		"uk":                   "GB",
		"canada":               "CA",
		"australia":            "AU",
		"germany":              "DE",
		"france":               "FR",
		"india":                "IN",
		"japan":                "JP",
		"mexico":               "MX",
		"brazil":               "BR",
		"singapore":            "SG",
		"hong kong":            "HK",
		"new zealand":          "NZ",
		"ireland":              "IE",
		"netherlands":          "NL",
		"spain":                "ES",
		"italy":                "IT",
		"south korea":          "KR",
		"korea":                "KR",
		"china":                "CN",
		"switzerland":          "CH",
		"sweden":               "SE",
		"norway":               "NO",
		"denmark":              "DK",
		"belgium":              "BE",
		"austria":              "AT",
		"poland":               "PL",
		"czech republic":       "CZ",
		"portugal":             "PT",
		"greece":               "GR",
		"turkey":               "TR",
		"israel":               "IL",
		"south africa":         "ZA",
		"pakistan":             "PK",
		"philippines":          "PH",
		"thailand":             "TH",
		"vietnam":              "VN",
		"indonesia":            "ID",
		"malaysia":             "MY",
		"argentina":            "AR",
		"chile":                "CL",
		"colombia":             "CO",
		"peru":                 "PE",
		"ukraine":              "UA",
		"russia":               "RU",
		"finland":              "FI",
		"romania":              "RO",
		"hungary":              "HU",
		"czech":                "CZ",
		"slovenia":             "SI",
		"croatia":              "HR",
		"serbia":               "RS",
		"middle east":          "AE",
		"united arab emirates": "AE",
		"uae":                  "AE",
		"qatar":                "QA",
		"saudi arabia":         "SA",
		"kuwait":               "KW",
		"bahrain":              "BH",
		"oman":                 "OM",
		"jordan":               "JO",
		"lebanon":              "LB",
		"egypt":                "EG",
		"morocco":              "MA",
		"algeria":              "DZ",
		"tunisia":              "TN",
		"kenya":                "KE",
		"nigeria":              "NG",
		"ghana":                "GH",
		"tanzania":             "TZ",
		"ethiopia":             "ET",
		"cameroon":             "CM",
	}

	normalized := strings.ToLower(strings.TrimSpace(countryName))
	if code, ok := codeMap[normalized]; ok {
		return code
	}

	// If not found, return first 2 chars uppercase as fallback
	if len(countryName) >= 2 {
		return strings.ToUpper(countryName[:2])
	}
	return countryName
}

// GetFormattedValue retrieves and formats a value from the user profile
func (f *FieldFormatter) GetFormattedValue(profileFieldName string, userProfile jobapplicationprofile.UserProfile) string {
	var value string

	switch profileFieldName {
	case "Email":
		value = userProfile.Email
	case "Phone":
		value = userProfile.Phone
	case "CountryOfResidence":
		value = userProfile.CountryOfResidence
	case "FirstName":
		value = userProfile.FirstName
	case "LastName":
		value = userProfile.LastName
	case "LinkedInUrl":
		if userProfile.LinkedInUrl != nil {
			value = *userProfile.LinkedInUrl
		}
	default:
		return ""
	}

	return f.FormatValue(profileFieldName, value, userProfile)
}
