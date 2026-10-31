package dashboard

import "embed"

//go:embed dashboard.html manager.html admin.html routing.html login.html user-dashboard.html welcome.html coming-soon.html management.html management-admin-*.html management-key-controls.js management-table-controls.js
var FS embed.FS
