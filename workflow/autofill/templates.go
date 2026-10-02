package autofill

import (
	"text/template"

	"github.com/SomtoJF/iris-worker/helper"
)

type TemplateSet struct {
	System *template.Template
	User   *template.Template
}

var Templates TemplateSet

func SetTemplates() {
	var err error

	Templates.System, err = helper.LoadTemplate("workflow/autofill/prompt/system.go.tmpl")
	if err != nil {
		panic(err)
	}
	Templates.User, err = helper.LoadTemplate("workflow/autofill/prompt/user.go.tmpl")
	if err != nil {
		panic(err)
	}
}
