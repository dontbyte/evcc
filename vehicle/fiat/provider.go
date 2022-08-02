package fiat

import (
	"fmt"
	"time"

	"github.com/evcc-io/evcc/api"
	"github.com/evcc-io/evcc/provider"
)

const refreshTimeout = 2 * time.Minute

type Provider struct {
	statusG     func() (interface{}, error)
	action      func(action, cmd string) (ActionResponse, error)
	expiry      time.Duration
	refreshId   string
	refreshTime time.Time
}

func NewProvider(api *API, vin, pin string, expiry, cache time.Duration) *Provider {
	impl := &Provider{
		statusG: provider.NewCached(func() (interface{}, error) {
			return api.Status(vin)
		}, cache).InterfaceGetter(),
		action: func(action, cmd string) (ActionResponse, error) {
			return api.Action(vin, pin, action, cmd)
		},
		expiry: expiry,
	}

	// use pin for refreshing
	if pin != "" {
		impl.statusG = provider.NewCached(func() (interface{}, error) {
			return impl.status(
				func() (StatusResponse, error) { return api.Status(vin) },
			)
		}, cache).InterfaceGetter()
	}

	return impl
}

func (v *Provider) deepRefresh() (string, error) {
	res, err := v.action("ev", "DEEPREFRESH")
	if err == nil && res.ResponseStatus != "pending" {
		err = fmt.Errorf("invalid response status: %s", res.ResponseStatus)
	}
	return res.CorrelationId, err
}

func (v *Provider) status(statusG func() (StatusResponse, error)) (StatusResponse, error) {
	res, err := statusG()

	// handle refresh
	if err == nil {
		// result expired?
		if res.Timestamp.Add(v.expiry).Before(time.Now()) {
			// start refresh
			if v.refreshId == "" {
				v.refreshId, err = v.deepRefresh()
				if err != nil {
					return res, err
				}

				v.refreshTime = time.Now()
				return res, api.ErrMustRetry
			}

			// wait for refresh
			if time.Since(v.refreshTime) > refreshTimeout {
				v.refreshId = ""
				return res, api.ErrTimeout
			}

			return res, api.ErrMustRetry
		}

		// refresh done
		v.refreshId = ""
	}

	return res, err
}

// SoC implements the api.Vehicle interface
func (v *Provider) SoC() (float64, error) {
	res, err := v.statusG()
	if res, ok := res.(StatusResponse); err == nil && ok {
		return res.EvInfo.Battery.StateOfCharge, nil
	}

	return 0, err
}

var _ api.VehicleRange = (*Provider)(nil)

// Range implements the api.VehicleRange interface
func (v *Provider) Range() (int64, error) {
	res, err := v.statusG()
	if res, ok := res.(StatusResponse); err == nil && ok {
		return int64(res.EvInfo.Battery.DistanceToEmpty.Value), nil
	}

	return 0, err
}

var _ api.VehicleOdometer = (*Provider)(nil)

// Odometer implements the api.VehicleOdometer interface
func (v *Provider) Odometer() (float64, error) {
	res, err := v.statusG()
	if res, ok := res.(StatusResponse); err == nil && ok {
		return float64(res.VehicleInfo.Odometer.Odometer.Value), nil
	}

	return 0, err
}

var _ api.ChargeState = (*Provider)(nil)

// Status implements the api.ChargeState interface
func (v *Provider) Status() (api.ChargeStatus, error) {
	status := api.StatusA // disconnected

	res, err := v.statusG()
	if res, ok := res.(StatusResponse); err == nil && ok {
		if res.EvInfo.Battery.PlugInStatus {
			status = api.StatusB // connected, not charging
		}
		if res.EvInfo.Battery.ChargingStatus == "CHARGING" {
			status = api.StatusC // charging
		}
	}

	return status, err
}
