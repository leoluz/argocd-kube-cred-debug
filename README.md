Small application that extracts the Argo CD code used to communicate with external clusters using the KubeAPI.
This application was used to troubleshoot the "server has asked for the client to provide credentials" error while communicating with external clusters.
This particular error can be raised by client-go if there is any authentication problem while sending requests to KubeAPI (e.g. using an invalid token).
However in our infra, this was an intermittent error happening in ~1% of the overall requests sent to KubeAPI.
