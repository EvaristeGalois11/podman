package common

import (
	"net"

	"github.com/containers/common/pkg/completion"
	"github.com/containers/podman/v3/cmd/podman/parse"
	"github.com/containers/podman/v3/libpod/define"
	"github.com/containers/podman/v3/libpod/network/types"
	"github.com/containers/podman/v3/pkg/domain/entities"
	"github.com/containers/podman/v3/pkg/specgen"
	"github.com/containers/podman/v3/pkg/specgenutil"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func DefineNetFlags(cmd *cobra.Command) {
	netFlags := cmd.Flags()

	addHostFlagName := "add-host"
	netFlags.StringSlice(
		addHostFlagName, []string{},
		"Add a custom host-to-IP mapping (host:ip) (default [])",
	)
	_ = cmd.RegisterFlagCompletionFunc(addHostFlagName, completion.AutocompleteNone)

	dnsFlagName := "dns"
	netFlags.StringSlice(
		dnsFlagName, containerConfig.DNSServers(),
		"Set custom DNS servers",
	)
	_ = cmd.RegisterFlagCompletionFunc(dnsFlagName, completion.AutocompleteNone)

	dnsOptFlagName := "dns-opt"
	netFlags.StringSlice(
		dnsOptFlagName, containerConfig.DNSOptions(),
		"Set custom DNS options",
	)
	_ = cmd.RegisterFlagCompletionFunc(dnsOptFlagName, completion.AutocompleteNone)

	dnsSearchFlagName := "dns-search"
	netFlags.StringSlice(
		dnsSearchFlagName, containerConfig.DNSSearches(),
		"Set custom DNS search domains",
	)
	_ = cmd.RegisterFlagCompletionFunc(dnsSearchFlagName, completion.AutocompleteNone)

	ipFlagName := "ip"
	netFlags.String(
		ipFlagName, "",
		"Specify a static IPv4 address for the container",
	)
	_ = cmd.RegisterFlagCompletionFunc(ipFlagName, completion.AutocompleteNone)

	macAddressFlagName := "mac-address"
	netFlags.String(
		macAddressFlagName, "",
		"Container MAC address (e.g. 92:d0:c6:0a:29:33)",
	)
	_ = cmd.RegisterFlagCompletionFunc(macAddressFlagName, completion.AutocompleteNone)

	networkFlagName := "network"
	netFlags.String(
		networkFlagName, containerConfig.NetNS(),
		"Connect a container to a network",
	)
	_ = cmd.RegisterFlagCompletionFunc(networkFlagName, AutocompleteNetworkFlag)

	networkAliasFlagName := "network-alias"
	netFlags.StringSlice(
		networkAliasFlagName, []string{},
		"Add network-scoped alias for the container",
	)
	_ = cmd.RegisterFlagCompletionFunc(networkAliasFlagName, completion.AutocompleteNone)

	publishFlagName := "publish"
	netFlags.StringSliceP(
		publishFlagName, "p", []string{},
		"Publish a container's port, or a range of ports, to the host (default [])",
	)
	_ = cmd.RegisterFlagCompletionFunc(publishFlagName, completion.AutocompleteNone)

	netFlags.Bool(
		"no-hosts", containerConfig.Containers.NoHosts,
		"Do not create /etc/hosts within the container, instead use the version from the image",
	)
}

// NetFlagsToNetOptions parses the network flags for the given cmd.
// The netnsFromConfig bool is used to indicate if the --network flag
// should always be parsed regardless if it was set on the cli.
func NetFlagsToNetOptions(opts *entities.NetOptions, flags pflag.FlagSet, netnsFromConfig bool) (*entities.NetOptions, error) {
	var (
		err error
	)
	if opts == nil {
		opts = &entities.NetOptions{}
	}

	opts.AddHosts, err = flags.GetStringSlice("add-host")
	if err != nil {
		return nil, err
	}
	// Verify the additional hosts are in correct format
	for _, host := range opts.AddHosts {
		if _, err := parse.ValidateExtraHost(host); err != nil {
			return nil, err
		}
	}

	servers, err := flags.GetStringSlice("dns")
	if err != nil {
		return nil, err
	}
	for _, d := range servers {
		if d == "none" {
			opts.UseImageResolvConf = true
			if len(servers) > 1 {
				return nil, errors.Errorf("%s is not allowed to be specified with other DNS ip addresses", d)
			}
			break
		}
		dns := net.ParseIP(d)
		if dns == nil {
			return nil, errors.Errorf("%s is not an ip address", d)
		}
		opts.DNSServers = append(opts.DNSServers, dns)
	}

	options, err := flags.GetStringSlice("dns-opt")
	if err != nil {
		return nil, err
	}
	opts.DNSOptions = options

	dnsSearches, err := flags.GetStringSlice("dns-search")
	if err != nil {
		return nil, err
	}
	// Validate domains are good
	for _, dom := range dnsSearches {
		if dom == "." {
			if len(dnsSearches) > 1 {
				return nil, errors.Errorf("cannot pass additional search domains when also specifying '.'")
			}
			continue
		}
		if _, err := parse.ValidateDomain(dom); err != nil {
			return nil, err
		}
	}
	opts.DNSSearch = dnsSearches

	inputPorts, err := flags.GetStringSlice("publish")
	if err != nil {
		return nil, err
	}
	if len(inputPorts) > 0 {
		opts.PublishPorts, err = specgenutil.CreatePortBindings(inputPorts)
		if err != nil {
			return nil, err
		}
	}

	opts.NoHosts, err = flags.GetBool("no-hosts")
	if err != nil {
		return nil, err
	}

	// parse the --network value only when the flag is set or we need to use
	// the netns config value, e.g. when --pod is not used
	if netnsFromConfig || flags.Changed("network") {
		network, err := flags.GetString("network")
		if err != nil {
			return nil, err
		}

		ns, networks, options, err := specgen.ParseNetworkString(network)
		if err != nil {
			return nil, err
		}

		if len(options) > 0 {
			opts.NetworkOptions = options
		}
		opts.Network = ns
		opts.Networks = networks
	}

	ip, err := flags.GetString("ip")
	if err != nil {
		return nil, err
	}
	if ip != "" {
		staticIP := net.ParseIP(ip)
		if staticIP == nil {
			return nil, errors.Errorf("%s is not an ip address", ip)
		}
		if !opts.Network.IsBridge() {
			return nil, errors.Wrap(define.ErrInvalidArg, "--ip can only be set when the network mode is bridge")
		}
		if len(opts.Networks) != 1 {
			return nil, errors.Wrap(define.ErrInvalidArg, "--ip can only be set for a single network")
		}
		for name, netOpts := range opts.Networks {
			netOpts.StaticIPs = append(netOpts.StaticIPs, staticIP)
			opts.Networks[name] = netOpts
		}
	}

	m, err := flags.GetString("mac-address")
	if err != nil {
		return nil, err
	}
	if len(m) > 0 {
		mac, err := net.ParseMAC(m)
		if err != nil {
			return nil, err
		}
		if !opts.Network.IsBridge() {
			return nil, errors.Wrap(define.ErrInvalidArg, "--mac-address can only be set when the network mode is bridge")
		}
		if len(opts.Networks) != 1 {
			return nil, errors.Wrap(define.ErrInvalidArg, "--mac-address can only be set for a single network")
		}
		for name, netOpts := range opts.Networks {
			netOpts.StaticMAC = types.HardwareAddr(mac)
			opts.Networks[name] = netOpts
		}
	}

	aliases, err := flags.GetStringSlice("network-alias")
	if err != nil {
		return nil, err
	}
	if len(aliases) > 0 {
		if !opts.Network.IsBridge() {
			return nil, errors.Wrap(define.ErrInvalidArg, "--network-alias can only be set when the network mode is bridge")
		}
		for name, netOpts := range opts.Networks {
			netOpts.Aliases = aliases
			opts.Networks[name] = netOpts
		}
	}

	return opts, err
}
