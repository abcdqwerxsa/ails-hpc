import { mapOrgToK8sNamespace } from "../lib/auth";

const GO_API_BASE = "http://192.168.20.226:8090/api/v1";

export interface CreateHpcJobPayload {
  orgSlug: string;
  jobName: string;
  image: string;
  slots: number;
  command: string[];
}

export async function submitHpcJob(data: CreateHpcJobPayload) {
  const tenantInfo = mapOrgToK8sNamespace(data.orgSlug);

  const res = await fetch(`${GO_API_BASE}/hpcjobs`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      name: data.jobName,
      namespace: tenantInfo.namespace,
      tenantNamespace: tenantInfo.namespace,
      queue: "user-queue",
      image: data.image,
      slots: data.slots,
      command: data.command,
    }),
  });

  if (!res.ok) {
    const errText = await res.text();
    throw new Error(`Failed to submit job to Go Core API: ${errText}`);
  }

  return await res.json();
}

export async function getHpcQueues() {
  const res = await fetch(`${GO_API_BASE}/queues`);
  if (!res.ok) {
    throw new Error("Failed to fetch queues status");
  }
  return await res.json();
}
