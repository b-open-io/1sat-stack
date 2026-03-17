package bsocial

// Swagger stubs for BSocial overlay routes.
// Actual handlers provided by go-overlay-services, mounted at /bsocial/overlay/.

// bsocialListTopicManagers lists BSocial topic managers
// @Summary List BSocial topic managers
// @Description Returns BSocial overlay topic managers
// @Tags bsocial-overlay
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /bsocial/overlay/listTopicManagers [get]
func bsocialListTopicManagers() {}

// bsocialListLookupServiceProviders lists BSocial lookup service providers
// @Summary List BSocial lookup services
// @Description Returns BSocial overlay lookup service providers
// @Tags bsocial-overlay
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /bsocial/overlay/listLookupServiceProviders [get]
func bsocialListLookupServiceProviders() {}

// bsocialSubmitTransaction submits a transaction to the BSocial overlay engine
// @Summary Submit transaction to BSocial overlay
// @Description Submit a transaction to the BSocial overlay engine for processing
// @Tags bsocial-overlay
// @Accept application/octet-stream
// @Produce json
// @Param x-topics header []string true "Topic names to submit to"
// @Param transaction body []byte true "Serialized transaction data"
// @Success 200 {object} map[string]interface{} "STEAK response"
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /bsocial/overlay/submit [post]
func bsocialSubmitTransaction() {}

// bsocialLookupQuestion performs a BSocial overlay lookup query
// @Summary BSocial overlay lookup
// @Description Query the BSocial overlay lookup service
// @Tags bsocial-overlay
// @Accept json
// @Produce json
// @Param query body object true "Lookup query"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /bsocial/overlay/lookup [post]
func bsocialLookupQuestion() {}

// bsocialRequestSyncResponse requests sync data from BSocial overlay
// @Summary BSocial overlay sync response
// @Description Request GASP synchronization data from BSocial overlay
// @Tags bsocial-overlay
// @Accept json
// @Produce json
// @Param X-BSV-Topic header string true "Topic identifier"
// @Param request body object true "Sync request"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /bsocial/overlay/requestSyncResponse [post]
func bsocialRequestSyncResponse() {}

// bsocialRequestForeignGASPNode requests a foreign GASP node from BSocial overlay
// @Summary BSocial overlay foreign GASP node
// @Description Request a GASP node from the BSocial overlay
// @Tags bsocial-overlay
// @Accept json
// @Produce json
// @Param X-BSV-Topic header string true "Topic identifier"
// @Param request body object true "Request with graphID, txID, and outputIndex"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /bsocial/overlay/requestForeignGASPNode [post]
func bsocialRequestForeignGASPNode() {}
