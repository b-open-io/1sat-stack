package ordlock

// Swagger stubs for market overlay routes.
// Actual handlers provided by go-overlay-services, mounted at /market/overlay/.

// marketListTopicManagers lists market topic managers
// @Summary List market topic managers
// @Description Returns market overlay topic managers
// @Tags market-overlay
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /overlay/listTopicManagers [get]
func marketListTopicManagers() {}

// marketListLookupServiceProviders lists market lookup service providers
// @Summary List market lookup services
// @Description Returns market overlay lookup service providers
// @Tags market-overlay
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /overlay/listLookupServiceProviders [get]
func marketListLookupServiceProviders() {}

// marketSubmitTransaction submits a transaction to the market overlay engine
// @Summary Submit transaction to market overlay
// @Description Submit a transaction to the market overlay engine for processing
// @Tags market-overlay
// @Accept application/octet-stream
// @Produce json
// @Param x-topics header []string true "Topic names to submit to"
// @Param transaction body []byte true "Serialized transaction data"
// @Success 200 {object} map[string]interface{} "STEAK response"
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /overlay/submit [post]
func marketSubmitTransaction() {}

// marketLookupQuestion performs a market overlay lookup query
// @Summary Market overlay lookup
// @Description Query the market overlay lookup service
// @Tags market-overlay
// @Accept json
// @Produce json
// @Param query body object true "Lookup query"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /overlay/lookup [post]
func marketLookupQuestion() {}

// marketRequestSyncResponse requests sync data from market overlay
// @Summary Market overlay sync response
// @Description Request GASP synchronization data from market overlay
// @Tags market-overlay
// @Accept json
// @Produce json
// @Param X-BSV-Topic header string true "Topic identifier"
// @Param request body object true "Sync request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /overlay/requestSyncResponse [post]
func marketRequestSyncResponse() {}

// marketRequestForeignGASPNode requests a foreign GASP node from market overlay
// @Summary Market overlay foreign GASP node
// @Description Request a GASP node from the market overlay
// @Tags market-overlay
// @Accept json
// @Produce json
// @Param X-BSV-Topic header string true "Topic identifier"
// @Param request body object true "Request with graphID, txID, and outputIndex"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /overlay/requestForeignGASPNode [post]
func marketRequestForeignGASPNode() {}
