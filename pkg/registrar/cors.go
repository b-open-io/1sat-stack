package registrar

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
)

// DefaultCORS returns the CORS middleware every 1sat-stack composition
// should apply app-wide: permissive origins with the BRC-103/104 auth and
// BRC-105 payment headers allowed and exposed.
func DefaultCORS() fiber.Handler {
	return cors.New(cors.Config{
		AllowOrigins: "*",
		AllowMethods: "GET,POST,OPTIONS",
		AllowHeaders: "Content-Type,Authorization,X-CallbackUrl,X-CallbackToken," +
			"x-bsv-auth-version,x-bsv-auth-message-type,x-bsv-auth-identity-key," +
			"x-bsv-auth-nonce,x-bsv-auth-your-nonce,x-bsv-auth-signature," +
			"x-bsv-auth-request-id,x-bsv-auth-requested-certificates," +
			"X-BSV-Payment,X-BSV-Payment-Version,X-BSV-Payment-Satoshis-Required," +
			"X-BSV-Payment-Derivation-Prefix",
		ExposeHeaders: "x-bsv-auth-version,x-bsv-auth-message-type,x-bsv-auth-identity-key," +
			"x-bsv-auth-nonce,x-bsv-auth-your-nonce,x-bsv-auth-signature," +
			"x-bsv-auth-request-id,x-bsv-auth-requested-certificates," +
			"X-BSV-Payment-Satoshis-Required,X-BSV-Payment-Satoshis-Paid," +
			"X-BSV-Payment-Derivation-Prefix",
		AllowCredentials: false,
	})
}
