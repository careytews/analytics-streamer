
//
// Definition for external streamer
//

// Import KSonnet library.
local k = import "ksonnet.beta.2/k.libsonnet";

// Short-cuts to various objects in the KSonnet library.
local depl = k.extensions.v1beta1.deployment;
local container = depl.mixin.spec.template.spec.containersType;
local containerPort = container.portsType;
local mount = container.volumeMountsType;
local volume = depl.mixin.spec.template.spec.volumesType;
local resources = container.resourcesType;
local env = container.envType;
local secretDisk = volume.mixin.secret;
local svc = k.core.v1.service;
local svcPort = svc.mixin.spec.portsType;
local svcLabels = svc.mixin.metadata.labels;
local externalIp = svc.mixin.spec.loadBalancerIp;
local svcType = svc.mixin.spec.type;
local annotations = depl.mixin.spec.template.metadata.annotations;

local worker(config) = {

    local version = import "version.jsonnet",
    local name =  "streamer",

    name: "%s" % name,
    namespace: config.namespace,

    labels: {app: "analytics-%s" % (name), component: "analytics"},

    images: [config.containerBase + "/analytics-streamer:" + version],

    input: config.workers.queues.streamer.input,
    output: config.workers.queues.streamer.output,

    // Ports used by deployments
    ports:: [
        containerPort.newNamed("streamer", 3333)
    ],

    // Volumes - single volume containing the key
    volumeMounts:: [
        mount.new("keys", "/key") + mount.readOnly(true)
    ],

    // Environment variables
    envs:: [

        // Hostname of AMQP
        env.new("AMQP_BROKER", "amqp://guest:guest@amqp:5672/"),

        // Pathname of key file.
        env.new("KEY", "/key/key"),

        // Streamer settings
        env.new("PORT", "3333")

    ],

    // Container definition.
    containers:: [
        container.new("analytics-%s" % name, self.images[0]) +
            container.ports(self.ports) +
            container.volumeMounts(self.volumeMounts) +
            container.env(self.envs) +
            container.args([self.input] +
                           std.map(function(x) "output:" + x,
                                   self.output)) +
            container.mixin.resources.limits({
                memory: "128M", cpu: "0.85"
            }) +
            container.mixin.resources.requests({
                memory: "128M", cpu: "0.8"
            })
    ],

    // Volumes
    volumes:: [
        volume.name("keys") + secretDisk.secretName("streamer-creds")
    ],

    // Deployment definition
    deployments:: [
        depl.new("analytics-%s" % name,
                 config.workers.replicas.streamer.min,
                 self.containers,
                 self.labels) +
            depl.mixin.metadata.namespace($.namespace) +
            depl.mixin.spec.template.spec.volumes(self.volumes) +
	annotations({"prometheus.io/scrape": "true",
		     "prometheus.io/port": "8080"})
    ],

    // Ports declared on the service.
    svcPorts:: [
        svcPort.newNamed("stream", 3333, 3333) + svcPort.protocol("TCP")
    ],

    services:: [
        svc.new("streamer", {app: "analytics-%s" % name}, self.svcPorts) +
            svc.mixin.metadata.namespace($.namespace) +
            svcLabels(self.labels) +
            externalIp(config.addresses.streamer) + svcType("LoadBalancer")
    ],

    resources:
		if config.options.includeAnalytics then
			 self.deployments + self.services
		else [],
};

[worker]
