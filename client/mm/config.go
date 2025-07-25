package mm

import (
	"encoding/json"
	"fmt"
	"strconv"

	"decred.org/dcrdex/dex/utils"
)

// MarketMakingConfig is the overall configuration of the market maker.
type MarketMakingConfig struct {
	BotConfigs []*BotConfig `json:"botConfigs"`
	CexConfigs []*CEXConfig `json:"cexConfigs"`
}

func (cfg *MarketMakingConfig) Copy() *MarketMakingConfig {
	c := &MarketMakingConfig{
		BotConfigs: make([]*BotConfig, len(cfg.BotConfigs)),
		CexConfigs: make([]*CEXConfig, len(cfg.CexConfigs)),
	}
	copy(c.BotConfigs, cfg.BotConfigs)
	copy(c.CexConfigs, cfg.CexConfigs)
	return c
}

// CEXConfig is a configuration for connecting to a CEX API.
type CEXConfig struct {
	// Name is the name of the cex.
	Name string `json:"name"`
	// APIKey is the API key for the CEX.
	APIKey string `json:"apiKey"`
	// APISecret is the API secret for the CEX.
	APISecret string `json:"apiSecret"`
}

// AutoRebalanceConfig configures deposits and withdrawals by setting minimum
// transfer sizes. Minimum transfer sizes should be set to prevent excessive
// fees on high-fee blockchains. To calculate a minimum transfer size for an
// asset, choose a fee-loss tolerance <= your profit target. If you only wanted to
// lose a maximum of 1% to transfers, and the fees associated with a transfer
// are 350 Sats, then your minimum transfer size might be set to
// 350 * (1 / 0.01) = 35000 Sats.
// For low-fee assets, a transfer size of zero would be perfectly fine in most
// cases, but using a higher value prevents churn.
// For obvious reasons, minimum transfer sizes should never be more than the
// total amount allocated for trading.
// The way these are configured will probably be changed to better capture the
// reasoning above.
type AutoRebalanceConfig struct {
	MinBaseTransfer  uint64 `json:"minBaseTransfer"`
	MinQuoteTransfer uint64 `json:"minQuoteTransfer"`
	// InternalOnly means that the bot will only simulate transfers by
	// allocating unallocated funds to the bot's balance and never actually
	// perform deposits and withdrawals with the CEX.
	InternalOnly bool `json:"internalOnly"`
}

func (a *AutoRebalanceConfig) copy() *AutoRebalanceConfig {
	return &AutoRebalanceConfig{
		MinBaseTransfer:  a.MinBaseTransfer,
		MinQuoteTransfer: a.MinQuoteTransfer,
	}
}

// BotBalanceAllocation is the initial allocation of funds for a bot.
type BotBalanceAllocation struct {
	DEX map[uint32]uint64 `json:"dex"`
	CEX map[uint32]uint64 `json:"cex"`
}

func (b *BotBalanceAllocation) copy() *BotBalanceAllocation {
	return &BotBalanceAllocation{
		DEX: utils.CopyMap(b.DEX),
		CEX: utils.CopyMap(b.CEX),
	}
}

// BotInventoryDiffs is the amount of funds to add or remove from a bot's
// allocation.
type BotInventoryDiffs struct {
	DEX map[uint32]int64 `json:"dex"`
	CEX map[uint32]int64 `json:"cex"`
}

func (d *BotInventoryDiffs) copy() *BotInventoryDiffs {
	return &BotInventoryDiffs{
		DEX: utils.CopyMap(d.DEX),
		CEX: utils.CopyMap(d.CEX),
	}
}

// balanceDiffsToAllocations converts a BotInventoryDiffs to a
// BotBalanceAllocation by removing all negative diffs.
func balanceDiffsToAllocation(diffs *BotInventoryDiffs) *BotBalanceAllocation {
	allocations := &BotBalanceAllocation{
		DEX: make(map[uint32]uint64, len(diffs.DEX)),
		CEX: make(map[uint32]uint64, len(diffs.CEX)),
	}

	for assetID, diff := range diffs.DEX {
		if diff > 0 {
			allocations.DEX[assetID] += uint64(diff)
		}
	}
	for assetID, diff := range diffs.CEX {
		if diff > 0 {
			allocations.CEX[assetID] += uint64(diff)
		}
	}

	return allocations
}

// #### IMPORTANT ###
// If non-backwards compatible changes are made to the BotConfig, a new version
// should be created and the event log db should be updated to support both
// versions.

type rpcConfig struct {
	Alloc         *BotBalanceAllocation `json:"alloc"`
	AutoRebalance *AutoRebalanceConfig  `json:"autoRebalance"`
}

func (r *rpcConfig) copy() *rpcConfig {
	return &rpcConfig{
		Alloc:         r.Alloc.copy(),
		AutoRebalance: r.AutoRebalance.copy(),
	}
}

// BotConfig is the configuration for a market making bot.
// The balance fields are the initial amounts that will be reserved to use for
// this bot. As the bot trades, the amounts reserved for it will be updated.
type BotConfig struct {
	Host    string `json:"host"`
	BaseID  uint32 `json:"baseID"`
	QuoteID uint32 `json:"quoteID"`

	BaseWalletOptions  map[string]string `json:"baseWalletOptions"`
	QuoteWalletOptions map[string]string `json:"quoteWalletOptions"`

	CEXName string `json:"cexName"`

	// UIConfig is settings defined and used by the front end to determine
	// allocations.
	UIConfig json.RawMessage `json:"uiConfig,omitempty"`

	// RPCConfig can be used for file-based initial allocations and
	// auto-rebalance settings.
	RPCConfig *rpcConfig `json:"rpcConfig"`

	// LotSize is the lot size of the market at the time this configuration
	// was created. It is used to notify the user if the lot size changes
	// when they are starting the bot.
	LotSize uint64 `json:"lotSize"`

	// Only one of the following configs should be set
	BasicMMConfig        *BasicMarketMakingConfig `json:"basicMarketMakingConfig,omitempty"`
	SimpleArbConfig      *SimpleArbConfig         `json:"simpleArbConfig,omitempty"`
	ArbMarketMakerConfig *ArbMarketMakerConfig    `json:"arbMarketMakingConfig,omitempty"`
}

func (c *BotConfig) copy() *BotConfig {
	b := *c

	b.BaseWalletOptions = utils.CopyMap(c.BaseWalletOptions)
	b.QuoteWalletOptions = utils.CopyMap(c.QuoteWalletOptions)

	if c.UIConfig != nil {
		b.UIConfig = make(json.RawMessage, len(c.UIConfig))
		copy(b.UIConfig, c.UIConfig)
	}
	if c.RPCConfig != nil {
		b.RPCConfig = c.RPCConfig.copy()
	}
	if c.BasicMMConfig != nil {
		b.BasicMMConfig = c.BasicMMConfig.copy()
	}
	if c.SimpleArbConfig != nil {
		b.SimpleArbConfig = c.SimpleArbConfig.copy()
	}
	if c.ArbMarketMakerConfig != nil {
		b.ArbMarketMakerConfig = c.ArbMarketMakerConfig.copy()
	}

	return &b
}

// updateLotSize modifies the bot's configuration based on an update to the
// market's lot size.
func (c *BotConfig) updateLotSize(oldLotSize, newLotSize uint64) {
	if c.BasicMMConfig != nil {
		c.BasicMMConfig.updateLotSize(oldLotSize, newLotSize)
	} else if c.ArbMarketMakerConfig != nil {
		c.ArbMarketMakerConfig.updateLotSize(oldLotSize, newLotSize)
	}
}

func (c *BotConfig) validate() error {
	if c.BasicMMConfig != nil {
		return c.BasicMMConfig.validate()
	} else if c.SimpleArbConfig != nil {
		return c.SimpleArbConfig.validate()
	} else if c.ArbMarketMakerConfig != nil {
		return c.ArbMarketMakerConfig.validate(c.BaseID, c.QuoteID)
	}

	return fmt.Errorf("no bot config set")
}

func validateConfigUpdate(old, new *BotConfig) error {
	if (old.BasicMMConfig == nil) != (new.BasicMMConfig == nil) ||
		(old.SimpleArbConfig == nil) != (new.SimpleArbConfig == nil) ||
		(old.ArbMarketMakerConfig == nil) != (new.ArbMarketMakerConfig == nil) {
		return fmt.Errorf("cannot change bot type")
	}

	return new.validate()
}

func (c *BotConfig) requiresPriceOracle() bool {
	return c.BasicMMConfig != nil
}

func (c *BotConfig) requiresCEX() bool {
	return c.SimpleArbConfig != nil || c.ArbMarketMakerConfig != nil
}

// multiSplitBuffer returns the additional buffer to add to the order size
// when doing a multi-split. This only applies to the quote asset.
func (c *BotConfig) multiSplitBuffer() float64 {
	if c.QuoteWalletOptions == nil {
		return 0
	}
	multiSplitBuffer, ok := c.QuoteWalletOptions["multisplitbuffer"]
	if !ok {
		return 0
	}
	multiSplitBufferFloat, err := strconv.ParseFloat(multiSplitBuffer, 64)
	if err != nil {
		return 0
	}
	return multiSplitBufferFloat
}

// maxPlacements returns the max amount of placements this bot will place on
// either side of the market in an epoch.
func (c *BotConfig) maxPlacements() (buy, sell uint32) {
	switch {
	case c.SimpleArbConfig != nil:
		return 1, 1
	case c.ArbMarketMakerConfig != nil:
		return uint32(len(c.ArbMarketMakerConfig.BuyPlacements)), uint32(len(c.ArbMarketMakerConfig.SellPlacements))
	case c.BasicMMConfig != nil:
		return uint32(len(c.BasicMMConfig.BuyPlacements)), uint32(len(c.BasicMMConfig.SellPlacements))
	default:
		return 1, 1
	}
}

func dexMarketID(host string, base, quote uint32) string {
	return fmt.Sprintf("%s-%d-%d", host, base, quote)
}
