package configure

import (
	"fmt"
	"sort"
	"strings"

	"github.com/evcc-io/evcc/util/sponsor"
	"github.com/evcc-io/evcc/util/templates"
	"github.com/thoas/go-funk"
	"gopkg.in/yaml.v3"
)

// processDeviceSelection processes the the user selected device, check if it is an actual device and make sure the requirements are set
func (c *CmdConfigure) processDeviceSelection(deviceCategory DeviceCategory) (templates.Template, error) {
	templateItem := c.selectItem(deviceCategory)

	if templateItem.Description == c.localizedString("ItemNotPresent", nil) {
		return templateItem, c.errItemNotPresent
	}

	if err := c.processDeviceRequirements(templateItem); err != nil {
		return templateItem, err
	}

	return templateItem, nil
}

// processDeviceValues processes the user provided values, create a device configuration and check if it is a valid device
func (c *CmdConfigure) processDeviceValues(values map[string]interface{}, templateItem templates.Template, device device, deviceCategory DeviceCategory) (device, error) {
	c.addedDeviceIndex++

	device.Name = fmt.Sprintf("%s%d", DeviceCategories[deviceCategory].defaultName, c.addedDeviceIndex)
	device.Title = templateItem.Description
	device.LogLevel = templateItem.LogLevel
	for item, value := range values {
		if strings.ToLower(item) != "title" {
			continue
		}
		if len(value.(string)) > 0 {
			device.Title = value.(string)
		}
	}

	categoryWithUsage := deviceCategory == DeviceCategoryPVMeter || deviceCategory == DeviceCategoryBatteryMeter || deviceCategory == DeviceCategoryGridMeter

	fmt.Println()
	if categoryWithUsage {
		fmt.Println(c.localizedString("TestingDevice_TitleUsage", localizeMap{"Device": templateItem.Description, "Usage": deviceCategory.String()}))
	} else {
		fmt.Println(c.localizedString("TestingDevice_Title", localizeMap{"Device": templateItem.Description}))
	}

	deviceTest := DeviceTest{
		DeviceCategory: deviceCategory,
		Template:       templateItem,
		ConfigValues:   values,
	}

	testResult, err := deviceTest.Test()
	if err != nil {
		fmt.Println("  ", c.localizedString("Error", localizeMap{"Error": err}))
		fmt.Println()

		question := c.localizedString("TestingDevice_AddFailed", localizeMap{"Device": templateItem.Description})
		if categoryWithUsage {
			question = c.localizedString("TestingDevice_AddFailedUsage", localizeMap{"Device": templateItem.Description, "Usage": deviceCategory.String()})
		}
		if !c.askYesNo(question) {
			c.addedDeviceIndex--
			return device, c.errDeviceNotValid
		}
	} else {
		if deviceCategory == DeviceCategoryCharger && testResult == DeviceTestResultValid {
			device.ChargerHasMeter = true
		}
	}

	templateItem.Params = append(templateItem.Params, templates.Param{Name: "name", Value: device.Name})
	if !c.expandedMode {
		b, err := templateItem.RenderProxyWithValues(values, false)
		if err != nil {
			c.addedDeviceIndex--
			return device, err
		}

		device.Yaml = string(b)
	} else {
		for _, p := range templateItem.Params {
			if p.Name == "name" {
				values["name"] = p.Value
				templateItem.Render = fmt.Sprintf("name: {{ .name }}\n%s", templateItem.Render)
			}
		}
		b, _, err := templateItem.RenderResult(false, values)
		if err != nil {
			c.addedDeviceIndex--
			return device, err
		}

		device.Yaml = string(b)
	}

	return device, nil
}

// processDeviceRequirements handles device requirements
func (c *CmdConfigure) processDeviceRequirements(templateItem templates.Template) error {
	if len(templateItem.Requirements.Description.String(c.lang)) > 0 {
		fmt.Println(c.localizedString("Requirements_Title", nil))
		fmt.Println("  ", templateItem.Requirements.Description.String(c.lang))
		if len(templateItem.Requirements.URI) > 0 {
			fmt.Println("  " + c.localizedString("Requirements_More", nil) + " " + templateItem.Requirements.URI)
		}
	}

	// check if sponsorship is required
	if templateItem.Requirements.Sponsorship && c.configuration.config.SponsorToken == "" {
		fmt.Println("-- Sponsorship -----------------------------")
		fmt.Println()
		fmt.Println(c.localizedString("Requirements_Sponsorship_Title", nil))
		fmt.Println()
		if !c.askYesNo(c.localizedString("Requirements_Sponsorship_Token", nil)) {
			return c.errItemNotPresent
		}
		sponsortoken := c.askValue(question{
			label:    c.localizedString("Requirements_Sponsorship_Token_Input", nil),
			required: true})
		c.configuration.config.SponsorToken = sponsortoken
		sponsor.Subject = sponsortoken
		fmt.Println()
		fmt.Println("--------------------------------------------")
	}

	// check if we need to setup a HEMS
	if templateItem.Requirements.Hems != "" && funk.ContainsString(templates.HemsValueTypes, templateItem.Requirements.Hems) {
		switch templateItem.Requirements.Hems {
		case templates.HemsTypeSMA:
			c.configuration.config.Hems = "type: sma\nAllowControl: false\n"
		}
	}

	// check if we need to setup an EEBUS HEMS
	if templateItem.Requirements.Eebus {
		if c.configuration.config.EEBUS == "" {
			fmt.Println()
			fmt.Println("-- EEBUS -----------------------------------")
			fmt.Println()
			eebusConfig, err := c.eebusCertificate()
			if err != nil {
				return fmt.Errorf("%s: %s", c.localizedString("Requirements_EEBUS_Cert_Error", nil), err)
			}

			err = c.configureEEBus(eebusConfig)
			if err != nil {
				return err
			}

			eebusYaml, err := yaml.Marshal(eebusConfig)
			if err != nil {
				return err
			}
			c.configuration.config.EEBUS = string(eebusYaml)
			fmt.Println()
			fmt.Println("--------------------------------------------")
		}

		fmt.Println()
		fmt.Println(c.localizedString("Requirements_EEBUS_Pairing", nil))
		fmt.Scanln()
	}

	return nil
}

// fetchElements returns template items of a given class
func (c *CmdConfigure) fetchElements(deviceCategory DeviceCategory) []templates.Template {
	var items []templates.Template

	for _, tmpl := range templates.ByClass(DeviceCategories[deviceCategory].class.String()) {
		if len(tmpl.Params) == 0 || len(tmpl.Description) == 0 {
			continue
		}

		if deviceCategory == DeviceCategoryGuidedSetup {
			if tmpl.GuidedSetup.Enable {
				items = append(items, tmpl)
			}
		} else {
			if len(DeviceCategories[deviceCategory].categoryFilter) == 0 ||
				c.paramChoiceContains(tmpl.Params, templates.ParamUsage, DeviceCategories[deviceCategory].categoryFilter.String()) {
				items = append(items, tmpl)
			}
		}
	}

	sort.Slice(items[:], func(i, j int) bool {
		// sort generic templates to the bottom
		if items[i].Generic && !items[j].Generic {
			return false
		}
		if !items[i].Generic && items[j].Generic {
			return true
		}
		return strings.ToLower(items[i].Description) < strings.ToLower(items[j].Description)
	})

	return items
}

// paramChoiceContains is a helper function to check if a param choice contains a given value
func (c *CmdConfigure) paramChoiceContains(params []templates.Param, name, filter string) bool {
	choices := c.paramChoiceValues(params, name)

	return funk.ContainsString(choices, filter)
}

// paramChoiceValues provides a list of possible values for a param choice
func (c *CmdConfigure) paramChoiceValues(params []templates.Param, name string) []string {
	choices := []string{}

	for _, item := range params {
		if item.Name != name {
			continue
		}

		choices = append(choices, item.Choice...)
	}

	return choices
}

// processConfig processes an EVCC configuration item
// Returns:
//   a map with param name and values
func (c *CmdConfigure) processConfig(paramItems []templates.Param, deviceCategory DeviceCategory, includeAdvanced bool) map[string]interface{} {
	usageFilter := DeviceCategories[deviceCategory].categoryFilter

	additionalConfig := make(map[string]interface{})
	selectedModbusKey := ""

	fmt.Println()
	fmt.Println(c.localizedString("Config_Title", nil))
	fmt.Println()

	for _, param := range paramItems {
		if param.Name == templates.ParamModbus {
			// baudrate and comset defaults can be overwritten, as they are device specific
			deviceDefaultBaudrate := templates.ModbusParamValueBaudrate
			if param.Baudrate != 0 {
				deviceDefaultBaudrate = param.Baudrate
			}
			deviceDefaultComset := templates.ModbusParamValueComset
			if param.Comset != "" {
				deviceDefaultBaudrate = param.Baudrate
			}

			choices := []string{}
			choiceKeys := []string{}
			for _, choice := range param.Choice {
				switch choice {
				case templates.ModbusChoiceRS485:
					choices = append(choices, "Serial (USB-RS485 Adapter)")
					choiceKeys = append(choiceKeys, templates.ModbusKeyRS485Serial)
					choices = append(choices, "Serial (Ethernet-RS485 Adapter)")
					choiceKeys = append(choiceKeys, templates.ModbusKeyRS485TCPIP)
				case templates.ModbusChoiceTCPIP:
					choices = append(choices, "TCP/IP")
					choiceKeys = append(choiceKeys, templates.ModbusKeyTCPIP)
				}
			}

			if len(choices) > 0 {
				// ask for modbus address
				id := c.askValue(question{
					label:        "ID",
					help:         "Modbus ID",
					defaultValue: 1,
					valueType:    templates.ParamValueTypeNumber,
					required:     true})
				additionalConfig[templates.ModbusParamNameId] = id

				// ask for modbus interface type
				index := 0
				if len(choices) > 1 {
					index, _ = c.askChoice(c.localizedString("Config_ModbusInterface", nil), choices)
				}
				selectedModbusKey = choiceKeys[index]
				switch selectedModbusKey {
				case templates.ModbusKeyRS485Serial:
					device := c.askValue(question{
						label:        "Device",
						help:         "USB-RS485 Adapter Adresse",
						exampleValue: templates.ModbusParamValueDevice,
						required:     true})
					additionalConfig[templates.ModbusParamNameDevice] = device

					baudrate := c.askValue(question{
						label:        "Baudrate",
						defaultValue: deviceDefaultBaudrate,
						valueType:    templates.ParamValueTypeNumber,
						required:     true})
					additionalConfig[templates.ModbusParamNameBaudrate] = baudrate

					comset := c.askValue(question{
						label:        "ComSet",
						defaultValue: deviceDefaultComset,
						required:     true})
					additionalConfig[templates.ModbusParamNameComset] = comset

				case templates.ModbusKeyRS485TCPIP, templates.ModbusKeyTCPIP:
					if selectedModbusKey == templates.ModbusKeyRS485TCPIP {
						additionalConfig[templates.ModbusParamNameRTU] = "true"
					}
					host := c.askValue(question{
						label:        "Host",
						exampleValue: templates.ModbusParamValueHost,
						required:     true})
					additionalConfig[templates.ModbusParamNameHost] = host

					port := c.askValue(question{
						label:        "Port",
						defaultValue: templates.ModbusParamValuePort,
						valueType:    templates.ParamValueTypeNumber,
						required:     true})
					additionalConfig[templates.ModbusParamNamePort] = port
				}
			}
		} else if param.Name != templates.ParamUsage {
			if !includeAdvanced && param.Advanced {
				continue
			}

			userFriendly := c.userFriendlyTexts(param)
			value := c.askValue(question{
				label:        userFriendly.Name,
				defaultValue: userFriendly.Default,
				exampleValue: userFriendly.Example,
				help:         userFriendly.Help.String(c.lang),
				valueType:    userFriendly.ValueType,
				mask:         userFriendly.Mask,
				required:     userFriendly.Required})
			additionalConfig[param.Name] = value
		} else if param.Name == templates.ParamUsage {
			if usageFilter != "" {
				additionalConfig[param.Name] = usageFilter.String()
			}
		}
	}

	return additionalConfig
}
