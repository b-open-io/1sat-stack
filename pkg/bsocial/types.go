package bsocial

import "github.com/bitcoinschema/go-bmap"

var BSocialKey = "bsocial"

type IndexerTx struct {
	bmap.Tx
	Timestamp int64 `json:"timestamp"`
}

var OutputTypes = "friend,like,repost,post,message"
