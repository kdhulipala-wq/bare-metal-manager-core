# Machine Pre-flight Cleanup Before Ingestion

Host ingestion is designed to handle machines in most states on its own,
including correcting drifted settings it finds along the way. If you hit a
machine state ingestion cannot recover from, file it as an improvement
request rather than working around it silently.

That said, a few machine states are known to cause friction today,
particularly for machines that have been through a **force-delete** or that
belong to a **site that was rebuilt/repaved**. `machine force-delete` removes
NICo's knowledge of a machine but does not touch the machine itself, so the
machine comes back into discovery in the same state it was deleted in (see
[Force deleting and rebuilding NICo hosts](../playbooks/force_delete.md)).
The next ingestion attempt has to be able to handle that state, so it is
worth clearing up these known issues before re-ingesting.

This page is a checklist, not a substitute for
[BMC and Out-of-Band Setup](../getting-started/prerequisites/bmc-oob-setup.md)
or [Ingesting Hosts](ingesting-hosts.md), which cover first-time ingestion
prerequisites.

## Known issues and mitigations

### 1. Host identity is unstable across re-ingestion

**Symptom:** a machine re-ingested after a force-delete or repave is assigned
a different host identity than before, even though the physical hardware is
unchanged.

**Mitigation:** clear the host's TPM so NICo re-establishes identity cleanly
on discovery, using the admin CLI rather than manual Redfish calls:

```bash
nico-admin-cli redfish tpm-reset
```

See [`nico-admin-cli redfish tpm-reset`](../manuals/nico-admin-cli/commands/redfish/redfish-tpm-reset.md).
A reboot is required afterward for the clear to take effect; power-cycle the
host as described in [Rebooting a machine](../playbooks/machine_reboot.md).

Some causes of identity instability are being addressed directly in
ingestion (tracked internally as #3434), which may remove the need for a TPM
reset in a future release. Until then, or on older NICo versions, resetting
the TPM before re-ingestion is the known workaround.

### 2. BMC does not release its previous DHCP lease / IP conflicts after a site rebuild

**Symptom:** after rebuilding or repaving a NICo site, a host or DPU BMC
keeps the IP address assigned by the old NICo DHCP service, or a manually
reset BMC is later assigned an IP that conflicts with another machine still
holding a lease from the old site.

**Cause:** NICo does not take over an existing DHCP lease database when a
site is rebuilt, so leases issued by the previous site's DHCP service remain
outstanding until they expire on their own schedule.

**Mitigation:**

- Before repaving a site, let BMCs release their leases back to the
  existing NICo DHCP service rather than resetting them by hand — a manual
  BMC reset does not notify the old lease holder, which is what causes the
  conflict.
- Tune the Kea DHCP lease timers (`renew-timer`, `rebind-timer`,
  `valid-lifetime`) in the Kea configuration to shorten how long stale leases
  stay outstanding across a rebuild. This is a configuration change only and
  does not require a NICo code change.
- If a conflict has already occurred, resolve it manually by identifying the
  BMC holding the conflicting lease and clearing or reassigning it before
  re-ingesting the affected machines.

### 3. DPU BMC password does not match the expected default at ingestion

**Symptom:** ingestion fails to authenticate to a DPU BMC because the
password on the DPU no longer matches the factory default NICo expects, for
example after a previous NICo ingestion already rotated it.

**Mitigation:** either reset the DPU BMC password back to a known value, or
tell NICo the correct current password instead:

- Reset the DPU BMC to a known factory-default password, and list that value
  in `expected_machines.json` for the affected host (see
  [Expected Machines Manifest](../getting-started/prerequisites/bmc-oob-setup.md#expected-machines-manifest)).
- Or, update the credential NICo has on file for that BMC directly:

  ```bash
  nico-admin-cli credential add-bmc --kind=bmc-root --username admin --password <current-password> --mac-address <dpu-bmc-mac>
  ```

  See [`nico-admin-cli credential add-bmc`](../manuals/nico-admin-cli/commands/credential/credential-add-bmc.md).

## General pre-flight checklist

Before (re-)ingesting a machine, confirm:

- [ ] The host BMC and DPU BMC are both reachable over Redfish on the
      OOB network and answer to a known set of credentials.
- [ ] Those credentials are recorded in `expected_machines.json`, or updated
      via `nico-admin-cli credential add-bmc` if they differ from the
      original factory defaults.
- [ ] No other machine on the site is holding a conflicting DHCP lease for
      the IP the BMC is expected to receive.
- [ ] If the machine was previously ingested and identity issues are a
      concern, the TPM has been reset via `nico-admin-cli redfish tpm-reset`.
- [ ] The DPU is in a clean, discoverable state; see
      [DPU Lifecycle Management](../dpu-management/dpu-lifecycle-management.md)
      for returning a DPU to factory state via the preingestion BFB.

## Related documentation

- [BMC and Out-of-Band Setup](../getting-started/prerequisites/bmc-oob-setup.md)
- [Ingesting Hosts](ingesting-hosts.md)
- [Force deleting and rebuilding NICo hosts](../playbooks/force_delete.md)
- [DPU Lifecycle Management](../dpu-management/dpu-lifecycle-management.md)
- [Host ingestion failures playbook](../playbooks/stuck_objects/host_ingestion_failures.md)
</content>
