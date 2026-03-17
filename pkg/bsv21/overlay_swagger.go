package bsv21

// Swagger stubs for BSV21 overlay routes.
// Actual handlers provided by go-overlay-services, mounted at /bsv21/overlay/.

// bsv21ListTopicManagers lists BSV21 topic managers
// @Summary List BSV21 topic managers
// @Description Returns BSV21 overlay topic managers
// @Tags bsv21-overlay
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /bsv21/overlay/listTopicManagers [get]
func bsv21ListTopicManagers() {}

// bsv21ListLookupServiceProviders lists BSV21 lookup service providers
// @Summary List BSV21 lookup services
// @Description Returns BSV21 overlay lookup service providers
// @Tags bsv21-overlay
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /bsv21/overlay/listLookupServiceProviders [get]
func bsv21ListLookupServiceProviders() {}

// bsv21SubmitTransaction submits a transaction to the BSV21 overlay engine
// @Summary Submit transaction to BSV21 overlay
// @Description Submit a transaction to the BSV21 overlay engine for processing
// @Tags bsv21-overlay
// @Accept application/octet-stream
// @Produce json
// @Param x-topics header []string true "Topic names to submit to"
// @Param transaction body []byte true "Serialized transaction data"
// @Success 200 {object} map[string]interface{} "STEAK response"
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /bsv21/overlay/submit [post]
func bsv21SubmitTransaction() {}

// bsv21LookupQuestion performs a BSV21 overlay lookup query
// @Summary BSV21 overlay lookup
// @Description Query the BSV21 overlay lookup service
// @Tags bsv21-overlay
// @Accept json
// @Produce json
// @Param query body object true "Lookup query"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /bsv21/overlay/lookup [post]
func bsv21LookupQuestion() {}

// bsv21RequestSyncResponse requests sync data from BSV21 overlay
// @Summary BSV21 overlay sync response
// @Description Request GASP synchronization data from BSV21 overlay
// @Tags bsv21-overlay
// @Accept json
// @Produce json
// @Param X-BSV-Topic header string true "Topic identifier"
// @Param request body object true "Sync request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /bsv21/overlay/requestSyncResponse [post]
func bsv21RequestSyncResponse() {}

// bsv21RequestForeignGASPNode requests a foreign GASP node from BSV21 overlay
// @Summary BSV21 overlay foreign GASP node
// @Description Request a GASP node from the BSV21 overlay
// @Tags bsv21-overlay
// @Accept json
// @Produce json
// @Param X-BSV-Topic header string true "Topic identifier"
// @Param request body object true "Request with graphID, txID, and outputIndex"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /bsv21/overlay/requestForeignGASPNode [post]
func bsv21RequestForeignGASPNode() {}
