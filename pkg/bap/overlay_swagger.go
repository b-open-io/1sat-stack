package bap

// Swagger stubs for BAP overlay routes.
// Actual handlers provided by go-overlay-services, mounted at /bap/overlay/.

// bapListTopicManagers lists BAP topic managers
// @Summary List BAP topic managers
// @Description Returns BAP overlay topic managers
// @Tags bap-overlay
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /overlay/listTopicManagers [get]
func bapListTopicManagers() {}

// bapListLookupServiceProviders lists BAP lookup service providers
// @Summary List BAP lookup services
// @Description Returns BAP overlay lookup service providers
// @Tags bap-overlay
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /overlay/listLookupServiceProviders [get]
func bapListLookupServiceProviders() {}

// bapSubmitTransaction submits a transaction to the BAP overlay engine
// @Summary Submit transaction to BAP overlay
// @Description Submit a transaction to the BAP overlay engine for processing
// @Tags bap-overlay
// @Accept application/octet-stream
// @Produce json
// @Param x-topics header []string true "Topic names to submit to"
// @Param transaction body []byte true "Serialized transaction data"
// @Success 200 {object} map[string]interface{} "STEAK response"
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /overlay/submit [post]
func bapSubmitTransaction() {}

// bapLookupQuestion performs a BAP overlay lookup query
// @Summary BAP overlay lookup
// @Description Query the BAP overlay lookup service
// @Tags bap-overlay
// @Accept json
// @Produce json
// @Param query body object true "Lookup query"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /overlay/lookup [post]
func bapLookupQuestion() {}

// bapRequestSyncResponse requests sync data from BAP overlay
// @Summary BAP overlay sync response
// @Description Request GASP synchronization data from BAP overlay
// @Tags bap-overlay
// @Accept json
// @Produce json
// @Param X-BSV-Topic header string true "Topic identifier"
// @Param request body object true "Sync request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /overlay/requestSyncResponse [post]
func bapRequestSyncResponse() {}

// bapRequestForeignGASPNode requests a foreign GASP node from BAP overlay
// @Summary BAP overlay foreign GASP node
// @Description Request a GASP node from the BAP overlay
// @Tags bap-overlay
// @Accept json
// @Produce json
// @Param X-BSV-Topic header string true "Topic identifier"
// @Param request body object true "Request with graphID, txID, and outputIndex"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /overlay/requestForeignGASPNode [post]
func bapRequestForeignGASPNode() {}
