package opns

// Swagger stubs for OPNS overlay routes.
// Actual handlers provided by go-overlay-services, mounted at /opns/overlay/.

// opnsListTopicManagers lists OPNS topic managers
// @Summary List OPNS topic managers
// @Description Returns OPNS overlay topic managers
// @Tags opns-overlay
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /overlay/listTopicManagers [get]
func opnsListTopicManagers() {}

// opnsListLookupServiceProviders lists OPNS lookup service providers
// @Summary List OPNS lookup services
// @Description Returns OPNS overlay lookup service providers
// @Tags opns-overlay
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /overlay/listLookupServiceProviders [get]
func opnsListLookupServiceProviders() {}

// opnsSubmitTransaction submits a transaction to the OPNS overlay engine
// @Summary Submit transaction to OPNS overlay
// @Description Submit a transaction to the OPNS overlay engine for processing
// @Tags opns-overlay
// @Accept application/octet-stream
// @Produce json
// @Param x-topics header []string true "Topic names to submit to"
// @Param transaction body []byte true "Serialized transaction data"
// @Success 200 {object} map[string]interface{} "STEAK response"
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /overlay/submit [post]
func opnsSubmitTransaction() {}

// opnsLookupQuestion performs an OPNS overlay lookup query
// @Summary OPNS overlay lookup
// @Description Query the OPNS overlay lookup service
// @Tags opns-overlay
// @Accept json
// @Produce json
// @Param query body object true "Lookup query"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /overlay/lookup [post]
func opnsLookupQuestion() {}

// opnsRequestSyncResponse requests sync data from OPNS overlay
// @Summary OPNS overlay sync response
// @Description Request GASP synchronization data from OPNS overlay
// @Tags opns-overlay
// @Accept json
// @Produce json
// @Param X-BSV-Topic header string true "Topic identifier"
// @Param request body object true "Sync request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /overlay/requestSyncResponse [post]
func opnsRequestSyncResponse() {}

// opnsRequestForeignGASPNode requests a foreign GASP node from OPNS overlay
// @Summary OPNS overlay foreign GASP node
// @Description Request a GASP node from the OPNS overlay
// @Tags opns-overlay
// @Accept json
// @Produce json
// @Param X-BSV-Topic header string true "Topic identifier"
// @Param request body object true "Request with graphID, txID, and outputIndex"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /overlay/requestForeignGASPNode [post]
func opnsRequestForeignGASPNode() {}
