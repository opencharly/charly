package schema

import (
	"net"
	"struct"
	"list"
)

#CloudConfig: {
	@jsonschema(schema="http://json-schema.org/draft-04/schema#")

	matchN(57, [#base_config, #cc_ansible, #cc_apk_configure, #cc_apt_configure, #cc_apt_pipelining, #cc_ubuntu_autoinstall, #cc_bootcmd, #cc_byobu, #cc_ca_certs, #cc_chef, #cc_disable_ec2_metadata, #cc_disk_setup, #cc_fan, #cc_final_message, #cc_growpart, #cc_grub_dpkg, #cc_install_hotplug, #cc_keyboard, #cc_keys_to_console, #cc_landscape, #cc_locale, #cc_lxd, #cc_mcollective, #cc_mounts, #cc_ntp, #cc_package_update_upgrade_install, #cc_phone_home, #cc_power_state_change, #cc_puppet, #cc_raspberry_pi, #cc_resizefs, #cc_resolv_conf, #cc_rh_subscription, #cc_rsyslog, #cc_runcmd, #cc_salt_minion, #cc_scripts_vendor, #cc_seed_random, #cc_set_hostname, #cc_set_passwords, #cc_snap, #cc_spacewalk, #cc_ssh_authkey_fingerprints, #cc_ssh_import_id, #cc_ssh, #cc_timezone, #cc_ubuntu_drivers, #cc_ubuntu_pro, #cc_update_etc_hosts, #cc_update_hostname, #cc_users_groups, #cc_wireguard, #cc_write_files, #cc_yum_add_repo, #cc_zypper_add_repo, #reporting_config, #output_config]) & (null | bool | number | string | [...] | close({
		allow_public_ssh_keys?:      _
		ansible?:                    _
		apk_repos?:                  _
		apt?:                        _
		apt_pipelining?:             _
		apt_reboot_if_required?:     _
		apt_update?:                 _
		apt_upgrade?:                _
		authkey_hash?:               _
		autoinstall?:                _
		bootcmd?:                    _
		byobu_by_default?:           _
		"ca-certs"?:                 _
		ca_certs?:                   _
		chef?:                       _
		chpasswd?:                   _
		cloud_config_modules?:       _
		cloud_final_modules?:        _
		cloud_init_modules?:         _
		create_hostname_file?:       _
		device_aliases?:             _
		disable_ec2_metadata?:       _
		disable_root?:               _
		disable_root_opts?:          _
		disk_setup?:                 _
		drivers?:                    _
		fan?:                        _
		final_message?:              _
		fqdn?:                       _
		fs_setup?:                   _
		groups?:                     _
		growpart?:                   _
		"grub-dpkg"?:                _
		grub_dpkg?:                  _
		hostname?:                   _
		keyboard?:                   _
		landscape?:                  _
		"launch-index"?:             _
		locale?:                     _
		locale_configfile?:          _
		lxd?:                        _
		manage_etc_hosts?:           _
		manage_resolv_conf?:         _
		mcollective?:                _
		merge_how?:                  _
		merge_type?:                 _
		migrate?:                    _
		mount_default_fields?:       _
		mounts?:                     _
		no_ssh_fingerprints?:        _
		ntp?:                        _
		output?:                     _
		package_reboot_if_required?: _
		package_update?:             _
		package_upgrade?:            _
		packages?:                   _
		password?:                   _
		phone_home?:                 _
		power_state?:                _
		prefer_fqdn_over_hostname?:  _
		preserve_hostname?:          _
		puppet?:                     _
		random_seed?:                _
		reporting?:                  _
		resize_rootfs?:              _
		resolv_conf?:                _
		rh_subscription?:            _
		rpi?:                        _
		rsyslog?:                    _
		runcmd?:                     _
		salt_minion?:                _
		snap?:                       _
		spacewalk?:                  _
		ssh?:                        _
		ssh_authorized_keys?:        _
		ssh_deletekeys?:             _
		ssh_fp_console_blacklist?:   _
		ssh_genkeytypes?:            _
		ssh_import_id?:              _
		ssh_key_console_blacklist?:  _
		ssh_keys?:                   _
		ssh_publish_hostkeys?:       _
		ssh_pwauth?:                 _
		ssh_quiet_keygen?:           _
		swap?:                       _
		system_info?:                _
		timezone?:                   _
		ubuntu_advantage?:           _
		ubuntu_pro?:                 _
		updates?:                    _
		user?:                       _
		users?:                      _
		vendor_data?:                _
		version?:                    _
		wireguard?:                  _
		write_files?:                _
		yum_repo_dir?:               _
		yum_repos?:                  _
		zypper?:                     _
	}))

	#: "ansible.pull": matchN(1, [{
		url!:           _
		playbook_name!: _
		...
	}, {
		url!:            _
		playbook_names!: _
		...
	}]) & close({
		accept_host_key?:     bool
		clean?:               bool
		full?:                bool
		diff?:                bool
		ssh_common_args?:     string
		scp_extra_args?:      string
		sftp_extra_args?:     string
		private_key?:         string
		checkout?:            string
		module_path?:         string
		timeout?:             string
		url?:                 string
		connection?:          string
		vault_id?:            string
		vault_password_file?: string
		verify_commit?:       bool
		inventory?:           string
		module_name?:         string
		sleep?:               string
		tags?:                string
		skip_tags?:           string

		// Single playbook_name to run with ansible-pull
		playbook_name?: string

		// List of playbook_names to run with ansible-pull
		playbook_names?: [...string]
	})

	#: "apt_configure.mirror": [...close({
		arches!: [...string] & [_, ...]
		uri?: net.AbsURL
		search?: [...net.AbsURL] & [_, ...]
		search_dns?: bool
		keyid?:      string
		key?:        string
		keyserver?:  string
	})] & [_, ...]

	#: "ca_certs.properties": struct.MinFields(1) & close({
		"remove-defaults"?: bool

		// Remove default CA certificates if true. Default: ``false``.
		remove_defaults?: bool

		// List of trusted CA certificates to add.
		trusted?: [...string] & [_, ...]
	})

	#: "ubuntu_pro.properties": close({
		// Optional list of Ubuntu Pro services to enable. Any of: cc-eal,
		// cis, esm-infra, fips, fips-updates, livepatch. By default, a
		// given contract token will automatically enable a number of
		// services, use this list to supplement which services should
		// additionally be enabled. Any service unavailable on a given
		// Ubuntu release or unentitled in a given contract will remain
		// disabled. In Ubuntu Pro instances, if this list is given, then
		// only those services will be enabled, ignoring contract
		// defaults. Passing beta services here will cause an error.
		enable?: [...string]

		// Optional list of Ubuntu Pro beta services to enable. By
		// default, a given contract token will automatically enable a
		// number of services, use this list to supplement which services
		// should additionally be enabled. Any service unavailable on a
		// given Ubuntu release or unentitled in a given contract will
		// remain disabled. In Ubuntu Pro instances, if this list is
		// given, then only those services will be enabled, ignoring
		// contract defaults.
		enable_beta?: [...string]

		// Contract token obtained from https://ubuntu.com/pro to attach.
		// Required for non-Pro instances.
		token?: string

		// Ubuntu Pro features.
		features?: close({
			// Optional boolean for controlling if ua-auto-attach.service (in
			// Ubuntu Pro instances) will be attempted each boot. Default:
			// ``false``.
			disable_auto_attach?: bool
		})

		// Configuration settings or override Ubuntu Pro config.
		config?: {
			// Ubuntu Pro HTTP Proxy URL or null to unset.
			http_proxy?: null | net.AbsURL

			// Ubuntu Pro HTTPS Proxy URL or null to unset.
			https_proxy?: null | net.AbsURL

			// HTTP Proxy URL used for all APT repositories on a system or
			// null to unset. Stored at
			// ``/etc/apt/apt.conf.d/90ubuntu-advantage-aptproxy``.
			global_apt_http_proxy?: null | net.AbsURL

			// HTTPS Proxy URL used for all APT repositories on a system or
			// null to unset. Stored at
			// ``/etc/apt/apt.conf.d/90ubuntu-advantage-aptproxy``.
			global_apt_https_proxy?: null | net.AbsURL

			// HTTP Proxy URL used only for Ubuntu Pro APT repositories or
			// null to unset. Stored at
			// ``/etc/apt/apt.conf.d/90ubuntu-advantage-aptproxy``.
			ua_apt_http_proxy?: null | net.AbsURL

			// HTTPS Proxy URL used only for Ubuntu Pro APT repositories or
			// null to unset. Stored at
			// ``/etc/apt/apt.conf.d/90ubuntu-advantage-aptproxy``.
			ua_apt_https_proxy?: null | net.AbsURL
			...
		}
	})

	#: "users_groups.groups_by_groupname": null | bool | number | string | [...] | close({
		{[=~"^.+$"]: string | [...string] & [_, ...]}
	})

	#: "users_groups.user": matchN(1, [null | bool | number | string | [...] | {
		name!: _
		...
	}, null | bool | number | string | [...] | {
		snapuser!: _
		...
	}]) & (null | bool | number | string | [...] | close({
		// The user's login name. Required otherwise user creation will be
		// skipped for this user.
		name?: string

		// List of doas rules to add for a user. doas or opendoas must be
		// installed for rules to take effect.
		doas?: [...string] & [_, ...]

		// Optional. Date on which the user's account will be disabled.
		// Default: ``null``.
		expiredate?: string

		// Optional comment about the user, usually a comma-separated
		// string of real name and contact information.
		gecos?: string

		// Optional comma-separated string of groups to add the user to.
		groups?: matchN(1, [string, [...string] & [_, ...], {
			{[=~"^.+$"]: null}
			...
		}])

		// Optional home dir for user. Default: ``/home/<username>``.
		homedir?: string

		// Optional string representing the number of days until the user
		// is disabled.
		inactive?:      string
		"lock-passwd"?: bool

		// Disable password login. Default: ``true``.
		lock_passwd?:      bool
		"no-create-home"?: bool

		// Do not create home directory. Default: ``false``.
		no_create_home?: bool
		"no-log-init"?:  bool

		// Do not initialize lastlog and faillog for user. Default:
		// ``false``.
		no_log_init?:     bool
		"no-user-group"?: bool

		// Do not create group named after user. Default: ``false``.
		no_user_group?: bool

		// Hash of user password applied when user does not exist. This
		// will NOT be applied if the user already exists. To generate
		// this hash, run: ``mkpasswd --method=SHA-512 --rounds=500000``
		// **Note:** Your password might possibly be visible to
		// unprivileged users on your system, depending on your cloud's
		// security model. Check if your cloud's IMDS server is visible
		// from an unprivileged user to evaluate risk.
		passwd?:          string
		"hashed-passwd"?: string

		// Hash of user password to be applied. This will be applied even
		// if the user is preexisting. To generate this hash, run:
		// ``mkpasswd --method=SHA-512 --rounds=500000``. **Note:** Your
		// password might possibly be visible to unprivileged users on
		// your system, depending on your cloud's security model. Check
		// if your cloud's IMDS server is visible from an unprivileged
		// user to evaluate risk.
		hashed_passwd?:       string
		"plain-text-passwd"?: string

		// Clear text of user password to be applied. This will be applied
		// even if the user is preexisting. **Note:** SSH keys or
		// certificates are a safer choice for logging in to your system.
		// For local escalation, supplying a hashed password is a safer
		// choice than plain text. Your password might possibly be
		// visible to unprivileged users on your system, depending on
		// your cloud's security model. An exposed plain text password is
		// an immediate security concern. Check if your cloud's IMDS
		// server is visible from an unprivileged user to evaluate risk.
		plain_text_passwd?: string
		"create-groups"?:   bool

		// Boolean set ``false`` to disable creation of specified user
		// ``groups``. Default: ``true``.
		create_groups?:   bool
		"primary-group"?: string

		// Primary group for user. Default: ``<username>``.
		primary_group?:  string
		"selinux-user"?: string

		// SELinux user for user's login. Default: the default SELinux
		// user.
		selinux_user?: string

		// Path to the user's login shell. Default: the host system's
		// default shell.
		shell?: string

		// Specify an email address to create the user as a Snappy user
		// through ``snap create-user``. If an Ubuntu SSO account is
		// associated with the address, username and SSH keys will be
		// requested from there.
		snapuser?: string

		// List of SSH keys to add to user's authkeys file. Can not be
		// combined with **ssh_redirect_user**.
		ssh_authorized_keys?: [...string] & [_, ...]
		"ssh-authorized-keys"?: [...string] & [_, ...]
		"ssh-import-id"?: [...string] & [_, ...]

		// List of ssh ids to import for user. Can not be combined with
		// **ssh_redirect_user**. See the man page[1] for more details.
		// [1]
		// https://manpages.ubuntu.com/manpages/noble/en/man1/ssh-import-id.1.html.
		ssh_import_id?: [...string] & [_, ...]
		"ssh-redirect-user"?: bool

		// Boolean set to true to disable SSH logins for this user. When
		// specified, all cloud-provided public SSH keys will be set up
		// in a disabled state for this username. Any SSH login as this
		// username will timeout and prompt with a message to login
		// instead as the **default_username** for this instance.
		// Default: ``false``. This key can not be combined with
		// **ssh_import_id** or **ssh_authorized_keys**.
		ssh_redirect_user?: bool

		// Optional. Create user as system user with no home directory.
		// Default: ``false``.
		system?: bool
		sudo?: matchN(1, [null | string, [...null | string], bool])

		// The user's ID. Default value [system default].
		uid?: matchN(1, [int, string])
	}))

	#all_modules: "ansible" | "apk-configure" | "apk_configure" | "apt-configure" | "apt_configure" | "apt-pipelining" | "apt_pipelining" | "bootcmd" | "byobu" | "ca-certs" | "ca_certs" | "chef" | "disable-ec2-metadata" | "disable_ec2_metadata" | "disk-setup" | "disk_setup" | "fan" | "final-message" | "final_message" | "growpart" | "grub-dpkg" | "grub_dpkg" | "install-hotplug" | "install_hotplug" | "keyboard" | "keys-to-console" | "keys_to_console" | "landscape" | "locale" | "lxd" | "mcollective" | "mounts" | "ntp" | "package-update-upgrade-install" | "package_update_upgrade_install" | "phone-home" | "phone_home" | "power-state-change" | "power_state_change" | "puppet" | "raspberry_pi" | "reset-rmc" | "reset_rmc" | "resizefs" | "resolv-conf" | "resolv_conf" | "rh-subscription" | "rh_subscription" | "rsyslog" | "runcmd" | "salt-minion" | "salt_minion" | "scripts-per-boot" | "scripts_per_boot" | "scripts-per-instance" | "scripts_per_instance" | "scripts-per-once" | "scripts_per_once" | "scripts-user" | "scripts_user" | "scripts-vendor" | "scripts_vendor" | "seed-random" | "seed_random" | "set-hostname" | "set_hostname" | "set-passwords" | "set_passwords" | "snap" | "spacewalk" | "ssh" | "ssh-authkey-fingerprints" | "ssh_authkey_fingerprints" | "ssh-import-id" | "ssh_import_id" | "timezone" | "ubuntu-advantage" | "ubuntu_advantage" | "ubuntu-autoinstall" | "ubuntu_autoinstall" | "ubuntu-drivers" | "ubuntu_drivers" | "ubuntu_pro" | "update-etc-hosts" | "update_etc_hosts" | "update-hostname" | "update_hostname" | "users-groups" | "users_groups" | "wireguard" | "write-files" | "write_files" | "write-files-deferred" | "write_files_deferred" | "yum-add-repo" | "yum_add_repo" | "zypper-add-repo" | "zypper_add_repo"

	#base_config: {
		cloud_init_modules?:   #modules_definition
		cloud_config_modules?: #modules_definition
		cloud_final_modules?:  #modules_definition

		// The launch index for the specified cloud-config.
		"launch-index"?: int
		merge_how?:      #merge_definition
		merge_type?:     #merge_definition
		system_info?: {
			...
		}
		...
	}

	#cc_ansible: {
		ansible?: close({
			// The type of installation for ansible. It can be one of the
			// following values:
			// - ``distro``
			// - ``pip``.
			install_method?: "distro" | "pip"

			// User to run module commands as. If install_method: pip, the pip
			// install runs as this user as well.
			run_user?: string

			// Sets the ANSIBLE_CONFIG environment variable. If set, overrides
			// default config.
			ansible_config?: string
			setup_controller?: close({
				repositories?: [...close({
					path!:   string
					source!: string
				})]
				run_ansible?: [...{
					playbook_name?:            string
					playbook_dir?:             string
					become_password_file?:     string
					connection_password_file?: string
					list_hosts?:               bool
					syntax_check?:             bool
					timeout?:                  >=0
					vault_id?:                 string
					vault_password_file?:      string
					background?:               >=0
					check?:                    bool
					diff?:                     bool
					module_path?:              string
					poll?:                     >=0
					args?:                     string
					extra_vars?:               string
					forks?:                    >=0
					inventory?:                string
					scp_extra_args?:           string
					sftp_extra_args?:          string
					private_key?:              string
					connection?:               string
					module_name?:              string
					sleep?:                    string
					tags?:                     string
					skip_tags?:                string
					...
				}]
			})
			galaxy?: close({
				actions!: [...[...=~"^.*$"]]
			})
			package_name?: string

			// pull playbooks from a VCS repo and run them on the host
			pull?: matchN(1, [[...#."ansible.pull"], #."ansible.pull"])
		})
		...
	}

	#cc_apk_configure: {
		apk_repos?: struct.MinFields(1) & close({
			// By default, cloud-init will generate a new repositories file
			// ``/etc/apk/repositories`` based on any valid configuration
			// settings specified within a apk_repos section of cloud config.
			// To disable this behavior and preserve the repositories file
			// from the pristine image, set **preserve_repositories** to
			// ``true``.
			// The **preserve_repositories** option overrides all other config
			// keys that would alter ``/etc/apk/repositories``.
			preserve_repositories?: bool
			alpine_repo?: null | struct.MinFields(1) & close({
				// The base URL of an Alpine repository, or mirror, to download
				// official packages from. If not specified then it defaults to
				// ``https://alpine.global.ssl.fastly.net/alpine``.
				base_url?: string

				// Whether to add the Community repo to the repositories file. By
				// default the Community repo is not included.
				community_enabled?: bool

				// Whether to add the Testing repo to the repositories file. By
				// default the Testing repo is not included. It is only
				// recommended to use the Testing repo on a machine running the
				// ``Edge`` version of Alpine as packages installed from Testing
				// may have dependencies that conflict with those in non-Edge
				// Main or Community repos.
				testing_enabled?: bool

				// The Alpine version to use (e.g. ``v3.12`` or ``edge``).
				version!: string
			})

			// The base URL of an Alpine repository containing unofficial
			// packages.
			local_repo_base_url?: string
		})
		...
	}

	#cc_apt_configure: null | bool | number | string | [...] | {
		apt?: struct.MinFields(1) & close({
			// By default, cloud-init will generate a new sources list in
			// ``/etc/apt/sources.list.d`` based on any changes specified in
			// cloud config. To disable this behavior and preserve the
			// sources list from the pristine image, set
			// **preserve_sources_list** to ``true``.
			//
			// The **preserve_sources_list** option overrides all other config
			// keys that would alter ``sources.list`` or ``sources.list.d``,
			// **except** for additional sources to be added to
			// ``sources.list.d``.
			preserve_sources_list?: bool

			// Entries in the sources list can be disabled using
			// **disable_suites**, which takes a list of suites to be
			// disabled. If the string ``$RELEASE`` is present in a suite in
			// the **disable_suites** list, it will be replaced with the
			// release name. If a suite specified in **disable_suites** is
			// not present in ``sources.list`` it will be ignored. For
			// convenience, several aliases are provided for
			// **disable_suites**:
			// - ``updates`` => ``$RELEASE-updates``
			// - ``backports`` => ``$RELEASE-backports``
			// - ``security`` => ``$RELEASE-security``
			// - ``proposed`` => ``$RELEASE-proposed``
			// - ``release`` => ``$RELEASE``.
			//
			// When a suite is disabled using **disable_suites**, its entry in
			// ``sources.list`` is not deleted; it is just commented out.
			disable_suites?: list.UniqueItems() & [...string] & [_, ...]
			primary?:  #."apt_configure.mirror"
			security?: #."apt_configure.mirror"

			// All source entries in ``apt-sources`` that match regex in
			// **add_apt_repo_match** will be added to the system using
			// ``add-apt-repository``. If **add_apt_repo_match** is not
			// specified, it defaults to ``^[\w-]+:\w``.
			add_apt_repo_match?: string

			// Debconf additional configurations can be specified as a
			// dictionary under the **debconf_selections** config key, with
			// each key in the dict representing a different set of
			// configurations. The value of each key must be a string
			// containing all the debconf configurations that must be
			// applied. We will bundle all of the values and pass them to
			// **debconf-set-selections**. Therefore, each value line must be
			// a valid entry for ``debconf-set-selections``, meaning that
			// they must possess for distinct fields:
			//
			// ``pkgname question type answer``
			//
			// Where:
			// - ``pkgname`` is the name of the package.
			// - ``question`` the name of the questions.
			// - ``type`` is the type of question.
			// - ``answer`` is the value used to answer the question.
			//
			// For example: ``ippackage ippackage/ip string 127.0.01``.
			debconf_selections?: struct.MinFields(1) & close({
				{[=~"^.+$"]: string}
			})

			// Specifies a custom template for rendering ``sources.list`` . If
			// no **sources_list** template is given, cloud-init will use
			// sane default. Within this template, the following strings will
			// be replaced with the appropriate values:
			// - ``$MIRROR``
			// - ``$RELEASE``
			// - ``$PRIMARY``
			// - ``$SECURITY``
			// - ``$KEY_FILE``
			sources_list?: string

			// Specify configuration for apt, such as proxy configuration.
			// This configuration is specified as a string. For multi-line
			// APT configuration, make sure to follow YAML syntax.
			conf?: string

			// More convenient way to specify https APT proxy. https proxy url
			// is specified in the format
			// ``https://[[user][:pass]@]host[:port]/``.
			https_proxy?: string

			// More convenient way to specify http APT proxy. http proxy url
			// is specified in the format
			// ``http://[[user][:pass]@]host[:port]/``.
			http_proxy?: string

			// Alias for defining a http APT proxy.
			proxy?: string

			// More convenient way to specify ftp APT proxy. ftp proxy url is
			// specified in the format
			// ``ftp://[[user][:pass]@]host[:port]/``.
			ftp_proxy?: string

			// Source list entries can be specified as a dictionary under the
			// **sources** config key, with each key in the dict representing
			// a different source file. The key of each source entry will be
			// used as an id that can be referenced in other config entries,
			// as well as the filename for the source's configuration under
			// ``/etc/apt/sources.list.d``. If the name does not end with
			// ``.list``, it will be appended. If there is no configuration
			// for a key in **sources**, no file will be written, but the key
			// may still be referred to as an id in other **sources**
			// entries.
			//
			// Each entry under **sources** is a dictionary which may contain
			// any of the following optional keys:
			// - **source**: a sources.list entry (some variable replacements
			// apply).
			// - **keyid**: a key to import via shortid or fingerprint.
			// - **key**: a raw PGP key.
			// - **keyserver**: alternate keyserver to pull **keyid** key
			// from.
			// - **filename**: specify the name of the list file.
			// - **append**: If ``true``, append to sources file, otherwise
			// overwrite it. Default: ``true``.
			//
			// The **source** key supports variable replacements for the
			// following strings:
			// - ``$MIRROR``
			// - ``$PRIMARY``
			// - ``$SECURITY``
			// - ``$RELEASE``
			// - ``$KEY_FILE``
			sources?: close({
				{[=~"^.+$"]: struct.MinFields(1) & close({
					source?: string, keyid?: string, key?: string, keyserver?: string, filename?: string, append?: bool
				})
				}
			})
		})
		...
	}

	#cc_apt_pipelining: {
		apt_pipelining?: matchN(1, [int, bool, matchN(1, ["os", "none" | "unchanged"])])
		...
	}

	#cc_bootcmd: {
		bootcmd?: [...matchN(1, [[...string], string])] & [_, ...]
		...
	}

	#cc_byobu: {
		byobu_by_default?: "enable-system" | "enable-user" | "disable-system" | "disable-user" | "enable" | "disable" | "user" | "system"
		...
	}

	#cc_ca_certs: {
		ca_certs?:   #."ca_certs.properties"
		"ca-certs"?: #."ca_certs.properties"
		...
	}

	#cc_chef: {
		chef?: struct.MinFields(1) & close({
			// Create the necessary directories for chef to run. By default,
			// it creates the following directories:
			// - ``/etc/chef``
			// - ``/var/log/chef``
			// - ``/var/lib/chef``
			// - ``/var/chef/backup``
			// - ``/var/chef/cache``
			// - ``/var/run/chef``
			directories?: list.UniqueItems() & [...string] & [_, ...]

			// Optional path for Chef configuration file. Default:
			// ``/etc/chef/client.rb``
			config_path?: string

			// Optional string to be written to file validation_key. Special
			// value ``system`` means set use existing file.
			validation_cert?: string

			// Optional path for validation_cert. Default:
			// ``/etc/chef/validation.pem``.
			validation_key?: string

			// Path to write run_list and initial_attributes keys that should
			// also be present in this configuration. Default:
			// ``/etc/chef/firstboot.json``.
			firstboot_path?: string

			// Set true if we should run or not run chef (defaults to false,
			// unless a gem installed is requested where this will then
			// default to true).
			exec?: bool

			// Optional path for client_cert. Default:
			// ``/etc/chef/client.pem``.
			client_key?: string

			// Specifies the location of the secret key used by chef to
			// encrypt data items. By default, this path is set to null,
			// meaning that chef will have to look at the path
			// ``/etc/chef/encrypted_data_bag_secret`` for it.
			encrypted_data_bag_secret?: string

			// Specifies which environment chef will use. By default, it will
			// use the ``_default`` configuration.
			environment?: string

			// Specifies the location in which backup files are stored. By
			// default, it uses the ``/var/chef/backup`` location.
			file_backup_path?: string

			// Specifies the location in which chef cache files will be saved.
			// By default, it uses the ``/var/chef/cache`` location.
			file_cache_path?: string

			// Specifies the location in which some chef json data is stored.
			// By default, it uses the ``/etc/chef/firstboot.json`` location.
			json_attribs?: string

			// Defines the level of logging to be stored in the log file. By
			// default this value is set to ``:info``.
			log_level?: string

			// Specifies the location of the chef log file. By default, the
			// location is specified at ``/var/log/chef/client.log``.
			log_location?: string

			// The name of the node to run. By default, we will use th
			// instance id as the node name.
			node_name?: string

			// Omnibus URL if chef should be installed through Omnibus. By
			// default, it uses the ``https://www.chef.io/chef/install.sh``.
			omnibus_url?: string

			// The number of retries that will be attempted to reach the
			// Omnibus URL. Default: ``5``.
			omnibus_url_retries?: int

			// Optional version string to require for omnibus install.
			omnibus_version?: string

			// The location in which a process identification number (pid) is
			// saved. By default, it saves in the
			// ``/var/run/chef/client.pid`` location.
			pid_file?: string

			// The URL for the chef server.
			server_url?: string

			// Show time in chef logs.
			show_time?: bool

			// Set the verify mode for HTTPS requests. We can have two
			// possible values for this parameter:
			// - ``:verify_none``: No validation of SSL certificates.
			// - ``:verify_peer``: Validate all SSL certificates.
			//
			// By default, the parameter is set as ``:verify_none``.
			ssl_verify_mode?: string

			// The name of the chef-validator key that Chef Infra Client uses
			// to access the Chef Infra Server during the initial Chef Infra
			// Client run.
			validation_name?: string

			// If set to ``true``, forces chef installation, even if it is
			// already installed.
			force_install?: bool

			// Specify a list of initial attributes used by the cookbooks.
			initial_attributes?: {
				...
			}

			// The type of installation for chef. It can be one of the
			// following values:
			// - ``packages``
			// - ``gems``
			// - ``omnibus``
			install_type?: "packages" | "gems" | "omnibus"

			// A run list for a first boot json.
			run_list?: [...string]

			// string that indicates if user accepts or not license related to
			// some of chef products. See
			// https://docs.chef.io/licensing/accept/.
			chef_license?: "accept" | "accept-silent" | "accept-no-persist"
		})
		...
	}

	#cc_disable_ec2_metadata: {
		// Set true to disable IPv4 routes to EC2 metadata. Default:
		// ``false``.
		disable_ec2_metadata?: bool
		...
	}

	#cc_disk_setup: {
		device_aliases?: close({
			{[=~"^.+$"]: string}
		})
		disk_setup?: close({
			{[=~"^.+$"]: close({
				// Specifies the partition table type, either ``mbr`` or ``gpt``.
				// Default: ``mbr``.
				table_type?: "mbr" | "gpt"

				// If set to ``true``, a single partition using all the space on
				// the device will be created. If set to ``false``, no partitions
				// will be created. If set to ``remove``, any existing partition
				// table will be purged. Partitions can be specified by providing
				// a list to ``layout``, where each entry in the list is either a
				// size or a list containing a size and the numerical value for a
				// partition type. The size for partitions is specified in
				// **percentage** of disk space, not in bytes (e.g. a size of 33
				// would take up 1/3 of the disk space). The partition type
				// defaults to '83' (Linux partition), for other types of
				// partition, such as Linux swap, the type must be passed as part
				// of a list along with the size. Default: ``false``.
				layout?: matchN(1, ["remove", bool, [...matchN(1, [int, list.MaxItems(2) & [...matchN(>=1, [int, string])] & [_, _, ...]])]])

				// Controls whether this module tries to be safe about writing
				// partition tables or not. If ``overwrite: false`` is set, the
				// device will be checked for a partition table and for a file
				// system and if either is found, the operation will be skipped.
				// If ``overwrite: true`` is set, no checks will be performed.
				// Using ``overwrite: true`` is **dangerous** and can lead to
				// data loss, so double check that the correct device has been
				// specified if using this option. Default: ``false``.
				overwrite?: bool
			})
			}
		})
		fs_setup?: [...close({
			// Label for the filesystem.
			label?: string

			// Filesystem type to create. E.g., ``ext4`` or ``btrfs``.
			filesystem?: string

			// Specified either as a path or as an alias in the format
			// ``<alias name>.<y>`` where ``<y>`` denotes the partition
			// number on the device. If specifying device using the ``<alias
			// name>.<partition number>`` format, the value of **partition**
			// will be overwritten.
			device?: string

			// The partition can be specified by setting **partition** to the
			// desired partition number. The **partition** option may also be
			// set to ``auto``, in which this module will search for the
			// existence of a filesystem matching the **label**,
			// **filesystem** and **device** of the **fs_setup** entry and
			// will skip creating the filesystem if one is found. The
			// **partition** option may also be set to ``any``, in which case
			// any filesystem that matches **filesystem** and **device** will
			// cause this module to skip filesystem creation for the
			// **fs_setup** entry, regardless of **label** matching or not.
			// To write a filesystem directly to a device, use ``partition:
			// none``. ``partition: none`` will **always** write the
			// filesystem, even when the **label** and **filesystem** are
			// matched, and ``overwrite`` is ``false``.
			partition?: "auto" | "any" | "none"

			// If ``true``, overwrite any existing filesystem. Using
			// ``overwrite: true`` for filesystems is **dangerous** and can
			// lead to data loss, so double check the entry in **fs_setup**.
			// Default: ``false``.
			overwrite?: bool

			// Ignored unless **partition** is ``auto`` or ``any``. Default
			// ``false``.
			replace_fs?: string

			// Optional options to pass to the filesystem creation command.
			// Ignored if you using **cmd** directly.
			extra_opts?: string | [...string]

			// Optional command to run to create the filesystem. Can include
			// string substitutions of the other **fs_setup** config keys.
			// This is only necessary if you need to override the default
			// command.
			cmd?: string | [...string]
		})]
		...
	}

	#cc_fan: {
		fan?: close({
			// The fan configuration to use as a single multi-line string.
			config!: string

			// The path to write the fan configuration to. Default:
			// ``/etc/network/fan``.
			config_path?: string
		})
		...
	}

	#cc_final_message: {
		// The message to display at the end of the run.
		final_message?: string
		...
	}

	#cc_growpart: {
		growpart?: close({
			// The utility to use for resizing. Default: ``auto``
			//
			// Possible options:
			//
			// * ``auto`` - Use any available utility
			//
			// * ``growpart`` - Use growpart utility
			//
			// * ``gpart`` - Use BSD gpart utility
			//
			// * ``'off'`` - Take no action.
			mode?: matchN(1, ["auto" | "growpart" | "gpart" | "off", false])

			// The devices to resize. Each entry can either be the path to the
			// device's mountpoint in the filesystem or a path to the block
			// device in '/dev'. Default: ``[/]``.
			devices?: [...string]

			// If ``true``, ignore the presence of ``/etc/growroot-disabled``.
			// If ``false`` and the file exists, then don't resize. Default:
			// ``false``.
			ignore_growroot_disabled?: bool
		})
		...
	}

	#cc_grub_dpkg: {
		grub_dpkg?: close({
			// Whether to configure which device is used as the target for
			// grub installation. Default: ``false``.
			enabled?: bool

			// Device to use as target for grub installation. If unspecified,
			// ``grub-probe`` of ``/boot`` will be used to find the device.
			"grub-pc/install_devices"?: string

			// Sets values for **grub-pc/install_devices_empty**. If
			// unspecified, will be set to ``true`` if
			// **grub-pc/install_devices** is empty, otherwise ``false``.
			"grub-pc/install_devices_empty"?: bool | string

			// Partition to use as target for grub installation. If
			// unspecified, ``grub-probe`` of ``/boot/efi`` will be used to
			// find the partition.
			"grub-efi/install_devices"?: string
		})
		"grub-dpkg"?: {
			...
		}
		...
	}

	#cc_install_hotplug: {
		updates?: close({
			network?: close({
				when!: [..."boot-new-instance" | "boot-legacy" | "boot" | "hotplug"]
			})
		})
		...
	}

	#cc_keyboard: {
		keyboard?: close({
			// Required. Keyboard layout. Corresponds to XKBLAYOUT.
			layout!: string

			// Optional. Keyboard model. Corresponds to XKBMODEL. Default:
			// ``pc105``.
			model?: string

			// Required for Alpine Linux, optional otherwise. Keyboard
			// variant. Corresponds to XKBVARIANT.
			variant?: string

			// Optional. Keyboard options. Corresponds to XKBOPTIONS.
			options?: string
		})
		...
	}

	#cc_keys_to_console: {
		ssh?: close({
			// Set false to avoid printing SSH keys to system console.
			// Default: ``true``.
			emit_keys_to_console!: bool
		})

		// Avoid printing matching SSH key types to the system console.
		ssh_key_console_blacklist?: list.UniqueItems() & [...string]

		// Avoid printing matching SSH fingerprints to the system console.
		ssh_fp_console_blacklist?: list.UniqueItems() & [...string]
		...
	}

	#cc_landscape: {
		landscape?: close({
			client!: {
				// The Landscape server URL to connect to. Default:
				// ``https://landscape.canonical.com/message-system``.
				url?: string

				// The URL to perform lightweight exchange initiation with.
				// Default: ``https://landscape.canonical.com/ping``.
				ping_url?: string

				// The directory to store data files in. Default:
				// ``/var/lib/land‐scape/client/``.
				data_path?: string

				// The log level for the client. Default: ``info``.
				log_level?: "debug" | "info" | "warning" | "error" | "critical"

				// The title of this computer.
				computer_title!: string

				// The account this computer belongs to.
				account_name!: string

				// The account-wide key used for registering clients.
				registration_key?: string

				// Comma separated list of tag names to be sent to the server.
				tags?: =~"^[-_0-9a-zA-Z]+(,[-_0-9a-zA-Z]+)*$"

				// The URL of the HTTP proxy, if one is needed.
				http_proxy?: string

				// The URL of the HTTPS proxy, if one is needed.
				https_proxy?: string
				...
			}
		})
		...
	}

	#cc_locale: null | bool | number | string | [...] | {
		// The locale to set as the system's locale (e.g. ar_PS).
		locale?: bool | string

		// The file in which to write the locale configuration (defaults
		// to the distro's default location).
		locale_configfile?: string
		...
	}

	#cc_lxd: {
		lxd?: struct.MinFields(1) & close({
			// LXD init configuration values to provide to `lxd init --auto`
			// command. Can not be combined with **lxd.preseed**.
			init?: close({
				// IP address for LXD to listen on.
				network_address?: string

				// Network port to bind LXD to.
				network_port?: int

				// Storage backend to use. Default: ``dir``.
				storage_backend?: "zfs" | "dir" | "lvm" | "btrfs"

				// Setup device based storage using DEVICE.
				storage_create_device?: string

				// Setup loop based storage with SIZE in GB.
				storage_create_loop?: int

				// Name of storage pool to use or create.
				storage_pool?: string

				// The password required to add new clients.
				trust_password?: string
			})

			// LXD bridge configuration provided to setup the host lxd bridge.
			// Can not be combined with **lxd.preseed**.
			bridge?: close({
				// Whether to setup LXD bridge, use an existing bridge by **name**
				// or create a new bridge. `none` will avoid bridge setup,
				// `existing` will configure lxd to use the bring matching
				// **name** and `new` will create a new bridge.
				mode!: "none" | "existing" | "new"

				// Name of the LXD network bridge to attach or create. Default:
				// ``lxdbr0``.
				name?: string

				// Bridge MTU, defaults to LXD's default value.
				mtu?: int & >=-1

				// IPv4 address for the bridge. If set, **ipv4_netmask** key
				// required.
				ipv4_address?: string

				// Prefix length for the **ipv4_address** key. Required when
				// **ipv4_address** is set.
				ipv4_netmask?: int

				// First IPv4 address of the DHCP range for the network created.
				// This value will combined with **ipv4_dhcp_last** key to set
				// LXC **ipv4.dhcp.ranges**.
				ipv4_dhcp_first?: string

				// Last IPv4 address of the DHCP range for the network created.
				// This value will combined with **ipv4_dhcp_first** key to set
				// LXC **ipv4.dhcp.ranges**.
				ipv4_dhcp_last?: string

				// Number of DHCP leases to allocate within the range.
				// Automatically calculated based on `ipv4_dhcp_first` and
				// `ipv4_dhcp_last` when unset.
				ipv4_dhcp_leases?: int

				// Set ``true`` to NAT the IPv4 traffic allowing for a routed IPv4
				// network. Default: ``false``.
				ipv4_nat?: bool

				// IPv6 address for the bridge (CIDR notation). When set,
				// **ipv6_netmask** key is required. When absent, no IPv6 will be
				// configured.
				ipv6_address?: string

				// Prefix length for **ipv6_address** provided. Required when
				// **ipv6_address** is set.
				ipv6_netmask?: int

				// Whether to NAT. Default: ``false``.
				ipv6_nat?: bool

				// Domain to advertise to DHCP clients and use for DNS resolution.
				domain?: string
			})

			// Opaque LXD preseed YAML config passed via stdin to the command:
			// lxd init --preseed. See:
			// https://documentation.ubuntu.com/lxd/en/latest/howto/initialize/#non-interactive-configuration
			// or lxd init --dump for viable config. Can not be combined with
			// either **lxd.init** or **lxd.bridge**.
			preseed?: string
		})
		...
	}

	#cc_mcollective: {
		mcollective?: close({
			conf?: close({
				// Optional value of server public certificate which will be
				// written to ``/etc/mcollective/ssl/server-public.pem``.
				"public-cert"?: string

				// Optional value of server private certificate which will be
				// written to ``/etc/mcollective/ssl/server-private.pem``.
				"private-cert"?: string

				{[=~"^.+$"]: matchN(1, [bool, int, string])}
			})
		})
		...
	}

	#cc_mounts: {
		// List of lists. Each inner list entry is a list of
		// ``/etc/fstab`` mount declarations of the format: [ fs_spec,
		// fs_file, fs_vfstype, fs_mntops, fs_freq, fs_passno ]. A mount
		// declaration with less than 6 items will get remaining values
		// from **mount_default_fields**. A mount declaration with only
		// `fs_spec` and no `fs_file` mountpoint will be skipped.
		mounts?: [...list.MaxItems(6) & [...string] & [_, ...]] & [_, ...]

		// Default mount configuration for any mount entry with less than
		// 6 options provided. When specified, 6 items are required and
		// represent ``/etc/fstab`` entries. Default:
		// ``defaults,nofail,x-systemd.after=cloud-init-network.service,_netdev``.
		mount_default_fields?: list.MaxItems(6) & [...null | string] & [_, _, _, _, _, _, ...]
		swap?: close({
			// Path to the swap file to create.
			filename?: string

			// The size in bytes of the swap file, 'auto' or a human-readable
			// size abbreviation of the format <float_size><units> where
			// units are one of B, K, M, G or T. **WARNING: Attempts to use
			// IEC prefixes in your configuration prior to cloud-init version
			// 23.1 will result in unexpected behavior. SI prefixes names
			// (KB, MB) are required on pre-23.1 cloud-init, however IEC
			// values are used. In summary, assume 1KB == 1024B, not 1000B**.
			size?: matchN(1, ["auto", int, =~"^([0-9]+)?\\.?[0-9]+[BKMGT]$"])

			// The maxsize in bytes of the swap file.
			maxsize?: matchN(1, [int, =~"^([0-9]+)?\\.?[0-9]+[BKMGT]$"])
		})
		...
	}

	#cc_ntp: {
		ntp?: null | close({
			// List of ntp pools. If both pools and servers are empty, 4
			// default pool servers will be provided of the format
			// ``{0-3}.{distro}.pool.ntp.org``. NOTE: for Alpine Linux when
			// using the Busybox NTP client this setting will be ignored due
			// to the limited functionality of Busybox's ntpd.
			pools?: list.UniqueItems() & [...string]

			// List of ntp servers. If both pools and servers are empty, 4
			// default pool servers will be provided with the format
			// ``{0-3}.{distro}.pool.ntp.org``.
			servers?: list.UniqueItems() & [...string]

			// List of ntp peers.
			peers?: list.UniqueItems() & [...string]

			// List of CIDRs to allow.
			allow?: list.UniqueItems() & [...string]

			// Name of an NTP client to use to configure system NTP. When
			// unprovided or 'auto' the default client preferred by the
			// distribution will be used. The following built-in client names
			// can be used to override existing configuration defaults:
			// chrony, ntp, openntpd, ntpdate, systemd-timesyncd.
			ntp_client?: string

			// Attempt to enable ntp clients if set to True. If set to
			// ``false``, ntp client will not be configured or installed.
			enabled?: bool

			// Configuration settings or overrides for the **ntp_client**
			// specified.
			config?: struct.MinFields(1) & close({
				// The path to where the **ntp_client** configuration is written.
				confpath?: string

				// The executable name for the **ntp_client**. For example, ntp
				// service **check_exe** is 'ntpd' because it runs the ntpd
				// binary.
				check_exe?: string

				// List of packages needed to be installed for the selected
				// **ntp_client**.
				packages?: list.UniqueItems() & [...string]

				// The systemd or sysvinit service name used to start and stop the
				// **ntp_client** service.
				service_name?: string

				// Inline template allowing users to customize their
				// **ntp_client** configuration with the use of the Jinja
				// templating engine. The template content should start with ``##
				// template:jinja``. Within the template, you can utilize any of
				// the following ntp module config keys: **servers**, **pools**,
				// **allow**, and **peers**. Each cc_ntp schema config key and
				// expected value type is defined above.
				template?: string
			})
		})
		...
	}

	#cc_package_update_upgrade_install: {
		// An array containing either a package specification, or an
		// object consisting of a package manager key having a package
		// specification value . A package specification can be either a
		// package name or a list with two entries, the first being the
		// package name and the second being the specific package version
		// to install.
		packages?: [...matchN(1, [close({
			apt?: [...#package_item_definition]
			snap?: [...#package_item_definition]
		}), #package_item_definition])] & [_, ...]

		// Set ``true`` to update packages. Happens before upgrade or
		// install. Default: ``false``.
		package_update?: bool

		// Set ``true`` to upgrade packages. Happens before install.
		// Default: ``false``.
		package_upgrade?: bool

		// Set ``true`` to reboot the system if required by presence of
		// `/var/run/reboot-required`. Default: ``false``.
		package_reboot_if_required?: bool
		apt_update?:                 bool
		apt_upgrade?:                bool
		apt_reboot_if_required?:     bool
		...
	}

	#cc_phone_home: {
		phone_home?: close({
			// The URL to send the phone home data to.
			url!: net.AbsURL

			// A list of keys to post or ``all``. Default: ``all``.
			post?: matchN(1, ["all", [..."pub_key_rsa" | "pub_key_ecdsa" | "pub_key_ed25519" | "instance_id" | "hostname" | "fqdn"]])

			// The number of times to try sending the phone home data.
			// Default: ``10``.
			tries?: int
		})
		...
	}

	#cc_power_state_change: {
		power_state?: close({
			// Time in minutes to delay after cloud-init has finished. Can be
			// ``now`` or an integer specifying the number of minutes to
			// delay. Default: ``now``.
			delay?: matchN(1, [int & >=0, =~"^\\+?[0-9]+$", "now"])

			// Must be one of ``poweroff``, ``halt``, or ``reboot``.
			mode!: "poweroff" | "reboot" | "halt"

			// Optional message to display to the user when the system is
			// powering off or rebooting.
			message?: string

			// Time in seconds to wait for the cloud-init process to finish
			// before executing shutdown. Default: ``30``.
			timeout?: int

			// Apply state change only if condition is met. May be boolean
			// true (always met), false (never met), or a command string or
			// list to be executed. For command formatting, see the
			// documentation for ``cc_runcmd``. If exit code is 0, condition
			// is met, otherwise not. Default: ``true``.
			condition?: bool | string | [...]
		})
		...
	}

	#cc_puppet: {
		puppet?: close({
			// Whether or not to install puppet. Setting to ``false`` will
			// result in an error if puppet is not already present on the
			// system. Default: ``true``.
			install?: bool

			// Optional version to pass to the installer script or package
			// manager. If unset, the latest version from the repos will be
			// installed.
			version?: string

			// Valid values are ``packages`` and ``aio``. Agent packages from
			// the puppetlabs repositories can be installed by setting
			// ``aio``. Based on this setting, the default config/SSL/CSR
			// paths will be adjusted accordingly. Default: ``packages``.
			install_type?: "packages" | "aio"

			// Puppet collection to install if **install_type** is ``aio``.
			// This can be set to one of ``puppet`` (rolling release),
			// ``puppet6``, ``puppet7`` (or their nightly counterparts) in
			// order to install specific release streams.
			collection?: string

			// If **install_type** is ``aio``, change the url of the install
			// script.
			aio_install_url?: string

			// Whether to remove the puppetlabs repo after installation if
			// **install_type** is ``aio`` Default: ``true``.
			cleanup?: bool

			// The path to the puppet config file. Default depends on
			// **install_type**.
			conf_file?: string

			// The path to the puppet SSL directory. Default depends on
			// **install_type**.
			ssl_dir?: string

			// The path to the puppet csr attributes file. Default depends on
			// **install_type**.
			csr_attributes_path?: string

			// Name of the package to install if **install_type** is
			// ``packages``. Default: ``puppet``.
			package_name?: string

			// Whether or not to run puppet after configuration finishes. A
			// single manual run can be triggered by setting **exec** to
			// ``true``, and additional arguments can be passed to ``puppet
			// agent`` via the **exec_args** key (by default the agent will
			// execute with the ``--test`` flag). Default: ``false``.
			exec?: bool

			// A list of arguments to pass to 'puppet agent' if 'exec' is true
			// Default: ``['--test']``.
			exec_args?: [...string]

			// By default, the puppet service will be automatically enabled
			// after installation and set to automatically start on boot. To
			// override this in favor of manual puppet execution set
			// **start_service** to ``false``.
			start_service?: bool

			// Every key present in the conf object will be added to
			// puppet.conf. As such, section names should be one of:
			// ``main``, ``server``, ``agent`` or ``user`` and keys should be
			// valid puppet configuration options. The configuration is
			// specified as a dictionary containing high-level ``<section>``
			// keys and lists of ``<key>=<value>`` pairs within each section.
			// The ``certname`` key supports string substitutions for ``%i``
			// and ``%f``, corresponding to the instance id and fqdn of the
			// machine respectively.
			//
			// ``ca_cert`` is a special case. It won't be added to
			// puppet.conf. It holds the puppetserver certificate in pem
			// format. It should be a multi-line string (using the | YAML
			// notation for multi-line strings).
			conf?: close({
				main?: {
					...
				}
				server?: {
					...
				}
				agent?: {
					...
				}
				user?: {
					...
				}
				ca_cert?: string
			})

			// create a ``csr_attributes.yaml`` file for CSR attributes and
			// certificate extension requests. See
			// https://puppet.com/docs/puppet/latest/config_file_csr_attributes.html.
			csr_attributes?: close({
				custom_attributes?: {
					...
				}
				extension_requests?: {
					...
				}
			})
		})
		...
	}

	#cc_raspberry_pi: {
		rpi?: {
			interfaces?: {
				// Enable SPI interface. Default: ``false``.
				spi?: bool

				// Enable I2C interface. Default: ``false``.
				i2c?: bool

				// Enable serial console. Default: ``false``.
				serial?: matchN(1, [bool, {
					// Enable login shell to be accessible over serial. Default:
					// ``false``.
					console?: bool

					// Enable serial port hardware. Default: ``false``.
					hardware?: bool
					...
				}])

				// Enable 1-Wire interface. Default: ``false``.
				onewire?: bool
				...
			}

			// Enable Raspberry Pi USB Gadget mode. Default: ``false``.
			enable_usb_gadget?: bool
			...
		}
		...
	}

	#cc_resizefs: {
		// Whether to resize the root partition. ``noblock`` will resize
		// in the background. Default: ``true``.
		resize_rootfs?: true | false | "noblock"
		...
	}

	#cc_resolv_conf: {
		// Whether to manage the resolv.conf file. **resolv_conf** block
		// will be ignored unless this is set to ``true``. Default:
		// ``false``.
		manage_resolv_conf?: bool
		resolv_conf?: close({
			// A list of nameservers to use to be added as ``nameserver``
			// lines.
			nameservers?: [...]

			// A list of domains to be added ``search`` line.
			searchdomains?: [...]

			// The domain to be added as ``domain`` line.
			domain?: string

			// A list of IP addresses to be added to ``sortlist`` line.
			sortlist?: [...]

			// Key/value pairs of options to go under ``options`` heading. A
			// unary option should be specified as ``true``.
			options?: {
				...
			}
		})
		...
	}

	#cc_rh_subscription: {
		rh_subscription?: matchN(8, [matchN(0, [null | bool | number | string | [...] | {
			activation_key!:   _
			"activation-key"!: _
			...
		}]) & {
			...
		}, matchN(0, [null | bool | number | string | [...] | {
			auto_attach!:   _
			"auto-attach"!: _
			...
		}]) & {
			...
		}, matchN(0, [null | bool | number | string | [...] | {
			service_level!:   _
			"service-level"!: _
			...
		}]) & {
			...
		}, matchN(0, [null | bool | number | string | [...] | {
			add_pool!:   _
			"add-pool"!: _
			...
		}]) & {
			...
		}, matchN(0, [null | bool | number | string | [...] | {
			enable_repo!:   _
			"enable-repo"!: _
			...
		}]) & {
			...
		}, matchN(0, [null | bool | number | string | [...] | {
			disable_repo!:   _
			"disable-repo"!: _
			...
		}]) & {
			...
		}, matchN(0, [null | bool | number | string | [...] | {
			rhsm_baseurl!:   _
			"rhsm-baseurl"!: _
			...
		}]) & {
			...
		}, matchN(0, [null | bool | number | string | [...] | {
			server_hostname!:   _
			"server-hostname"!: _
			...
		}]) & {
			...
		}]) & close({
			// The username to use. Must be used with password. Should not be
			// used with **activation_key** or **org**.
			username?: string

			// The password to use. Must be used with username. Should not be
			// used with **activation_key** or **org**.
			password?:         string
			activation_key?:   #rh_subscription_activation_key
			"activation-key"?: #rh_subscription_activation_key

			// The organization to use. Must be used with **activation_key**.
			// Should not be used with **username** or **password**.
			org?: matchN(1, [string, int])
			auto_attach?:     #rh_subscription_auto_attach
			"auto-attach"?:   #rh_subscription_auto_attach
			service_level?:   #rh_subscription_service_level
			"service-level"?: #rh_subscription_service_level
			add_pool?:        #rh_subscription_add_pool
			"add-pool"?:      #rh_subscription_add_pool
			enable_repo?:     #rh_subscription_enable_repo
			"enable-repo"?:   #rh_subscription_enable_repo
			disable_repo?:    #rh_subscription_disable_repo
			"disable-repo"?:  #rh_subscription_disable_repo

			// Sets the release_version via``subscription-manager release
			// --set=<release_version>`` then deletes the package manager
			// cache ``/var/cache/{dnf,yum}`` . These steps are applied after
			// any pool attachment and/or enabling/disabling repos. For more
			// information about this key, check
			// https://access.redhat.com/solutions/238533 .
			release_version?:   string
			rhsm_baseurl?:      #rh_subscription_rhsm_baseurl
			"rhsm-baseurl"?:    #rh_subscription_rhsm_baseurl
			server_hostname?:   #rh_subscription_server_hostname
			"server-hostname"?: #rh_subscription_server_hostname
		})
		...
	}

	#cc_rsyslog: {
		rsyslog?: close({
			// The directory where rsyslog configuration files will be
			// written. Default: ``/etc/rsyslog.d``.
			config_dir?: string

			// The name of the rsyslog configuration file. Default:
			// ``20-cloud-config.conf``.
			config_filename?: string

			// Each entry in **configs** is either a string or an object. Each
			// config entry contains a configuration string and a file to
			// write it to. For config entries that are an object,
			// **filename** sets the target filename and **content**
			// specifies the config string to write. For config entries that
			// are only a string, the string is used as the config string to
			// write. If the filename to write the config to is not
			// specified, the value of the **config_filename** key is used. A
			// file with the selected filename will be written inside the
			// directory specified by **config_dir**.
			configs?: [...matchN(1, [string, close({
				filename?: string
				content!:  string
			})])]

			// Each key is the name for an rsyslog remote entry. Each value
			// holds the contents of the remote config for rsyslog. The
			// config consists of the following parts:
			//
			// - filter for log messages (defaults to ``*.*``)
			//
			// - optional leading ``@`` or ``@@``, indicating udp and tcp
			// respectively (defaults to ``@``, for udp)
			//
			// - ipv4 or ipv6 hostname or address. ipv6 addresses must be in
			// ``[::1]`` format, (e.g. ``@[fd00::1]:514``)
			//
			// - optional port number (defaults to ``514``)
			//
			// This module will provide sane defaults for any part of the
			// remote entry that is not specified, so in most cases remote
			// hosts can be specified just using ``<name>: <address>``.
			remotes?: {
				...
			}

			// The command to use to reload the rsyslog service after the
			// config has been updated. If this is set to ``auto``, then an
			// appropriate command for the distro will be used. This is the
			// default behavior. To manually set the command, use a list of
			// command args (e.g. ``[systemctl, restart, rsyslog]``).
			service_reload_command?: matchN(1, ["auto", [...string]])

			// Install rsyslog. Default: ``false``.
			install_rsyslog?: bool

			// The executable name for the rsyslog daemon.
			// For example, ``rsyslogd``, or ``/opt/sbin/rsyslogd`` if the
			// rsyslog binary is in an unusual path. This is only used if
			// ``install_rsyslog`` is ``true``. Default: ``rsyslogd``.
			check_exe?: string

			// List of packages needed to be installed for rsyslog. This is
			// only used if **install_rsyslog** is ``true``. Default:
			// ``[rsyslog]``.
			packages?: list.UniqueItems() & [...string]
		})
		...
	}

	#cc_runcmd: {
		runcmd?: [...matchN(1, [[...string], string, null])] & [_, ...]
		...
	}

	#cc_salt_minion: {
		salt_minion?: close({
			// Package name to install. Default: ``salt-minion``.
			pkg_name?: string

			// Service name to enable. Default: ``salt-minion``.
			service_name?: string

			// Directory to write config files to. Default: ``/etc/salt``.
			config_dir?: string

			// Configuration to be written to `config_dir`/minion.
			conf?: {
				...
			}

			// Configuration to be written to `config_dir`/grains.
			grains?: {
				...
			}

			// Public key to be used by the salt minion.
			public_key?: string

			// Private key to be used by salt minion.
			private_key?: string

			// Directory to write key files. Default: `config_dir`/pki/minion.
			pki_dir?: string
		})
		...
	}

	#cc_scripts_vendor: {
		vendor_data?: close({
			// Whether vendor-data is enabled or not. Default: ``true``.
			enabled?: bool | string

			// The command to run before any vendor scripts. Its primary use
			// case is for profiling a script, not to prevent its run.
			prefix?: string | [...int | string]
		})
		...
	}

	#cc_seed_random: {
		random_seed?: close({
			// File to write random data to. Default: ``/dev/urandom``.
			file?: string

			// This data will be written to **file** before data from the
			// datasource. When using a multi-line value or specifying binary
			// data, be sure to follow YAML syntax and use the ``|`` and
			// ``!binary`` YAML format specifiers when appropriate.
			data?: string

			// Used to decode **data** provided. Allowed values are ``raw``,
			// ``base64``, ``b64``, ``gzip``, or ``gz``. Default: ``raw``.
			encoding?: "raw" | "base64" | "b64" | "gzip" | "gz"

			// Execute this command to seed random. The command will have
			// RANDOM_SEED_FILE in its environment set to the value of
			// **file** above.
			command?: [...string]

			// If true, and **command** is not available to be run then an
			// exception is raised and cloud-init will record failure.
			// Otherwise, only debug error is mentioned. Default: ``false``.
			command_required?: bool
		})
		...
	}

	#cc_set_hostname: {
		// If true, the hostname will not be changed. Default: ``false``.
		preserve_hostname?: bool

		// The hostname to set.
		hostname?: string

		// The fully qualified domain name to set.
		fqdn?: string

		// If true, the fqdn will be used if it is set. If false, the
		// hostname will be used. If unset, the result is
		// distro-dependent.
		prefer_fqdn_over_hostname?: bool

		// If ``false``, the hostname file (e.g. /etc/hostname) will not
		// be created if it does not exist. On systems that use systemd,
		// setting create_hostname_file to ``false`` will set the
		// hostname transiently. If ``true``, the hostname file will
		// always be created and the hostname will be set statically on
		// systemd systems. Default: ``true``.
		create_hostname_file?: bool
		...
	}

	#cc_set_passwords: {
		// Sets whether or not to accept password authentication. ``true``
		// will enable password auth. ``false`` will disable. Default:
		// leave the value unchanged. In order for this config to be
		// applied, SSH may need to be restarted. On systemd systems,
		// this restart will only happen if the SSH service has already
		// been started. On non-systemd systems, a restart will be
		// attempted regardless of the service state.
		ssh_pwauth?: bool | string
		chpasswd?: close({
			// Whether to expire all user passwords such that a password will
			// need to be reset on the user's next login. Default: ``true``.
			expire?: bool

			// This key represents a list of existing users to set passwords
			// for. Each item under users contains the following required
			// keys: **name** and **password** or in the case of a randomly
			// generated password, **name** and **type**. The **type** key
			// has a default value of ``hash``, and may alternatively be set
			// to ``text`` or ``RANDOM``. Randomly generated passwords may be
			// insecure, use at your own risk.
			users?: [...matchN(>=1, [close({
				name!: string
				type!: "RANDOM"
			}), close({
				name!:     string
				type?:     "hash" | "text"
				password!: string
			})])]
			list?: matchN(1, [string, [...=~"^.+:.+$"]]) & (string | [_, ...])
		})

		// Set the default user's password. Ignored if **chpasswd**
		// ``list`` is used.
		password?: string
		...
	}

	#cc_snap: {
		snap?: struct.MinFields(1) & close({
			// Properly-signed snap assertions which will run before and snap
			// **commands**.
			assertions?: list.UniqueItems() & [...string] & [_, ...] | struct.MinFields(1) & {
				[string]: string
			}

			// Snap commands to run on the target system.
			commands?: [...matchN(1, [string, [...string]])] & [_, ...] | struct.MinFields(1) & {
				[string]: matchN(1, [string, [...string]])
			}
		})
		...
	}

	#cc_spacewalk: {
		spacewalk?: close({
			// The Spacewalk server to use.
			server?: string

			// The proxy to use when connecting to Spacewalk.
			proxy?: string

			// The activation key to use when registering with Spacewalk.
			activation_key?: string
		})
		...
	}

	#cc_ssh: {
		// A dictionary entries for the public and private host keys of
		// each desired key type. Entries in the **ssh_keys** config dict
		// should have keys in the format ``<key type>_private``, ``<key
		// type>_public``, and, optionally, ``<key type>_certificate``,
		// e.g. ``rsa_private: <key>``, ``rsa_public: <key>``, and
		// ``rsa_certificate: <key>``. Not all key types have to be
		// specified, ones left unspecified will not be used. If this
		// config option is used, then separate keys will not be
		// automatically generated. In order to specify multi-line
		// private host keys and certificates, use YAML multi-line
		// syntax. **Note:** Your ssh keys might possibly be visible to
		// unprivileged users on your system, depending on your cloud's
		// security model.
		ssh_keys?: close({
			{[=~"^(ecdsa|ed25519|rsa)_(public|private|certificate)$"]: string}
		})

		// The SSH public keys to add ``.ssh/authorized_keys`` in the
		// default user's home directory.
		ssh_authorized_keys?: [_, ...] & [...string]

		// Remove host SSH keys. This prevents re-use of a private host
		// key from an image with default host SSH keys. Default:
		// ``true``.
		ssh_deletekeys?: bool

		// The SSH key types to generate. Default: ``[rsa, ecdsa,
		// ed25519]``.
		ssh_genkeytypes?: [_, ...] & [..."ecdsa" | "ed25519" | "rsa"]

		// Disable root login. Default: ``true``.
		disable_root?: bool

		// Disable root login options. If **disable_root_opts** is
		// specified and contains the string ``$USER``, it will be
		// replaced with the username of the default user. Default:
		// ``no-port-forwarding,no-agent-forwarding,no-X11-forwarding,command="echo
		// 'Please login as the user \"$USER\" rather than the user
		// \"$DISABLE_USER\".';echo;sleep 10;exit 142"``.
		disable_root_opts?: string

		// If ``true``, will import the public SSH keys from the
		// datasource's metadata to the user's ``.ssh/authorized_keys``
		// file. Default: ``true``.
		allow_public_ssh_keys?: bool

		// If ``true``, will suppress the output of key generation to the
		// console. Default: ``false``.
		ssh_quiet_keygen?: bool
		ssh_publish_hostkeys?: close({
			// If true, will read host keys from ``/etc/ssh/*.pub`` and
			// publish them to the datasource (if supported). Default:
			// ``true``.
			enabled?: bool

			// The SSH key types to ignore when publishing. Default: ``[]`` to
			// publish all SSH key types.
			blacklist?: [...string]
		})
		...
	}

	#cc_ssh_authkey_fingerprints: {
		// If true, SSH fingerprints will not be written. Default:
		// ``false``.
		no_ssh_fingerprints?: bool

		// The hash type to use when generating SSH fingerprints. Default:
		// ``sha256``.
		authkey_hash?: string
		...
	}

	#cc_ssh_import_id: {
		ssh_import_id?: [...string]
		...
	}

	#cc_timezone: {
		// The timezone to use as represented in /usr/share/zoneinfo.
		timezone?: string
		...
	}

	#cc_ubuntu_autoinstall: {
		// Cloud-init ignores this key and its values. It is used by
		// Subiquity, the Ubuntu Autoinstaller. See:
		// https://ubuntu.com/server/docs/install/autoinstall-reference.
		autoinstall?: {
			version!: int
			...
		}
		...
	}

	#cc_ubuntu_drivers: {
		drivers?: close({
			nvidia?: close({
				// Do you accept the NVIDIA driver license?
				"license-accepted"!: bool

				// The version of the driver to install (e.g. "390", "410").
				// Default: latest version.
				version?: string
			})
		})
		...
	}

	#cc_ubuntu_pro: {
		ubuntu_pro?:       #."ubuntu_pro.properties"
		ubuntu_advantage?: #."ubuntu_pro.properties"
		...
	}

	#cc_update_etc_hosts: {
		// Whether to manage ``/etc/hosts`` on the system. If ``true``,
		// render the hosts file using
		// ``/etc/cloud/templates/hosts.tmpl`` replacing ``$hostname``
		// and ``$fqdn``. If ``localhost``, append a ``127.0.1.1`` entry
		// that resolves from FQDN and hostname every boot. Default:
		// ``false``.
		manage_etc_hosts?: matchN(1, [true | false | "localhost", "template"])

		// Optional fully qualified domain name to use when updating
		// ``/etc/hosts``. Preferred over **hostname** if both are
		// provided. In absence of **hostname** and **fqdn** in
		// cloud-config, the ``local-hostname`` value will be used from
		// datasource metadata.
		fqdn?: string

		// Hostname to set when rendering ``/etc/hosts``. If **fqdn** is
		// set, the hostname extracted from **fqdn** overrides
		// **hostname**.
		hostname?: string
		...
	}

	#cc_update_hostname: {
		// Do not update system hostname when ``true``. Default:
		// ``false``.
		preserve_hostname?: bool

		// By default, it is distro-dependent whether cloud-init uses the
		// short hostname or fully qualified domain name when both
		// ``local-hostname` and ``fqdn`` are both present in instance
		// metadata. When set ``true``, use fully qualified domain name
		// if present as hostname instead of short hostname. When set
		// ``false``, use **hostname** config value if present, otherwise
		// fallback to **fqdn**.
		prefer_fqdn_over_hostname?: bool

		// If ``false``, the hostname file (e.g. /etc/hostname) will not
		// be created if it does not exist. On systems that use systemd,
		// setting create_hostname_file to ``false`` will set the
		// hostname transiently. If ``true``, the hostname file will
		// always be created and the hostname will be set statically on
		// systemd systems. Default: ``true``.
		create_hostname_file?: bool
		...
	}

	#cc_users_groups: {
		groups?: #."users_groups.groups_by_groupname"

		// The **user** dictionary values override the **default_user**
		// configuration from ``/etc/cloud/cloud.cfg``. The **user**
		// dictionary keys supported for the default_user are the same as
		// the **users** schema.
		user?: matchN(1, [string, #."users_groups.user"])
		users?: string | [...matchN(1, [string, [...string], #."users_groups.user"])] | {
			...
		}
		...
	}

	#cc_wireguard: {
		wireguard?: null | struct.MinFields(1) & close({
			interfaces!: [...close({
				// Name of the interface. Typically wgx (example: wg0).
				name?: string

				// Path to configuration file of Wireguard interface.
				config_path?: string

				// Wireguard interface configuration. Contains key, peer, ...
				content?: string
			})] & [_, ...]

			// List of shell commands to be executed as probes.
			readinessprobe?: list.UniqueItems() & [...string]
		})
		...
	}

	#cc_write_files: {
		write_files?: [...close({
			// Path of the file to which **content** is decoded and written.
			path!: string

			// Optional content to write to the provided **path**. When
			// content is present and encoding is not 'text/plain', decode
			// the content prior to writing. Default: ``''``.
			content?: string

			// Optional specification for content loading from an arbitrary
			// URI.
			source?: close({
				// URI from which to load file content. If loading fails
				// repeatedly, **content** is used instead.
				uri!: net.AbsURL

				// Optional HTTP headers to accompany load request, if applicable.
				headers?: [string]: string
			})

			// Optional owner:group to chown on the file and new directories.
			// Default: ``root:root``.
			owner?: string

			// Optional file permissions to set on **path** represented as an
			// octal string '0###'. Default: ``0o644``.
			permissions?: string

			// Optional encoding type of the content. Default: ``text/plain``.
			// No decoding is performed by default. Supported encoding types
			// are: gz, gzip, gz+base64, gzip+base64, gz+b64, gzip+b64, b64,
			// base64.
			encoding?: "gz" | "gzip" | "gz+base64" | "gzip+base64" | "gz+b64" | "gzip+b64" | "b64" | "base64" | "text/plain"

			// Whether to append **content** to existing file if **path**
			// exists. Default: ``false``.
			append?: bool

			// Defer writing the file until 'final' stage, after users were
			// created, and packages were installed. Default: ``false``.
			defer?: bool
		})] & [_, ...]
		...
	}

	#cc_yum_add_repo: {
		// The repo parts directory where individual yum repo config files
		// will be written. Default: ``/etc/yum.repos.d``.
		yum_repo_dir?: string
		yum_repos?: struct.MinFields(1) & close({
			{[=~"^[0-9a-zA-Z -_]+$"]: matchN(>=1, [{
				baseurl!: _, ...
			}, {
				metalink!: _, ...
			}, {
				mirrorlist!: _, ...
			}]) & close({
				// URL to the directory where the yum repository's 'repodata'
				// directory lives.
				baseurl?: net.AbsURL

				// Specifies a URL to a metalink file for the repomd.xml.
				metalink?: net.AbsURL

				// Specifies a URL to a file containing a baseurls list.
				mirrorlist?: net.AbsURL

				// Optional human-readable name of the yum repo.
				name?: string

				// Whether to enable the repo. Default: ``true``.
				enabled?: bool

				{[=~"^[0-9a-zA-Z_]+$"]: matchN(1, [int, bool, string])}
			})
			}
		})
		...
	}

	#cc_zypper_add_repo: {
		zypper?: struct.MinFields(1) & {
			repos?: [...{
				// The unique id of the repo, used when writing
				// /etc/zypp/repos.d/<id>.repo.
				id!: string

				// The base repositoy URL.
				baseurl!: net.AbsURL
				...
			}] & [_, ...]

			// Any supported zypo.conf key is written to
			// ``/etc/zypp/zypp.conf``.
			config?: {
				...
			}
			...
		}
		...
	}

	#merge_definition: matchN(1, [string, [_, ...] & [...close({
		name!: "list" | "dict" | "str"
		settings!: [..."allow_delete" | "no_replace" | "replace" | "append" | "prepend" | "recurse_dict" | "recurse_list" | "recurse_array" | "recurse_str"]
	})]])

	#modules_definition: [...matchN(1, [#all_modules, [...]])]

	#output_config: {
		output?: close({
			all?:    #output_log_operator
			init?:   #output_log_operator
			config?: #output_log_operator
			final?:  #output_log_operator
		})
		...
	}

	#output_log_operator: matchN(1, [string, list.MaxItems(2) & [...string] & [_, _, ...], close({
		// A filepath operation configuration. This is a string containing
		// a filepath and an optional leading operator: '>', '>>' or '|'.
		// Operators '>' and '>>' indicate whether to overwrite or append
		// to the file. The operator '|' redirects content to the command
		// arguments specified.
		output?: string

		// A filepath operation configuration. A string containing a
		// filepath and an optional leading operator: '>', '>>' or '|'.
		// Operators '>' and '>>' indicate whether to overwrite or append
		// to the file. The operator '|' redirects content to the command
		// arguments specified.
		error?: string
	})])

	#package_item_definition: matchN(1, [list.MaxItems(2) & [...string] & [_, _, ...], string])

	#reporting_config: {
		reporting?: close({
			{[=~"^.+$"]: matchN(1, [close({
				type!: "log", level?: "DEBUG" | "INFO" | "WARN" | "ERROR" | "FATAL"
			}), close({
				type!: "print"
			}), close({
				type!: "webhook"

				// The URL to send the event to.
				endpoint!: net.AbsURL

				// The consumer key to use for the webhook.
				consumer_key?: string

				// The token key to use for the webhook.
				token_key?: string

				// The token secret to use for the webhook.
				token_secret?: string

				// The consumer secret to use for the webhook.
				consumer_secret?: string

				// The timeout in seconds to wait for a response from the webhook.
				timeout?: >=0

				// The number of times to retry sending the webhook.
				retries?: int & >=0
			}), close({
				type!: "hyperv"

				// The path to the KVP file to use for the hyperv reporter.
				kvp_file_path?: string, event_types?: [...string]
			})])
			}
		})
		...
	}

	// The activation key to use. Must be used with **org**. Should
	// not be used with **username** or **password**.
	#rh_subscription_activation_key: string

	// A list of pool IDs add to the subscription.
	#rh_subscription_add_pool: [...string]

	// Whether to attach subscriptions automatically.
	#rh_subscription_auto_attach: bool

	// A list of repositories to disable.
	#rh_subscription_disable_repo: [...string]

	// A list of repositories to enable.
	#rh_subscription_enable_repo: [...string]

	// Sets the baseurl in ``/etc/rhsm/rhsm.conf``.
	#rh_subscription_rhsm_baseurl: string

	// Sets the serverurl in ``/etc/rhsm/rhsm.conf``.
	#rh_subscription_server_hostname: string

	// The service level to use when subscribing to RH repositories.
	// ``auto_attach`` must be true for this to be used.
	#rh_subscription_service_level: string
}
