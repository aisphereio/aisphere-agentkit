package routers

import (
	"net/http"

	"google.golang.org/adk/server/adkrest/controllers"
)

type AISphereAuthAPIRouter struct {
	controller *controllers.AISphereAuthAPIController
}

func NewAISphereAuthAPIRouter(controller *controllers.AISphereAuthAPIController) *AISphereAuthAPIRouter {
	return &AISphereAuthAPIRouter{controller: controller}
}

func (r *AISphereAuthAPIRouter) Routes() Routes {
	return Routes{
		{Name: "AISphereLogin", Methods: []string{http.MethodGet}, Pattern: "/auth/login", HandlerFunc: r.controller.LoginHandler},
		{Name: "AISphereLogout", Methods: []string{http.MethodPost}, Pattern: "/auth/logout", HandlerFunc: r.controller.LogoutHandler},
	}
}
