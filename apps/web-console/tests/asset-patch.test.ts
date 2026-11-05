import { describe, it, expect } from 'vitest';
import { assetChanges } from '../src/cloud/asset';
import type { AssetMasterData } from '../../../contracts/typescript/types.gen';

const base: AssetMasterData = {
  id: 'a1', projectId: 'P001', displayName: '一号门机', assetCode: 'CRN-1', assetType: 'GANTRY', assetStatus: 'ACTIVE',
  manufacturerId: 'm1', manufacturerSnapshot: '厂', brandSnapshot: 'B', modelSnapshot: 'M', serialNumber: 'SN',
  manufactureDate: '2020-01-02', installationDate: null, commissioningDate: null, liftingHeightM: 12.5,
  applicationType: null, controlMode: null, version: 3, specialEquipmentCode: null, useRegistrationNo: null,
};

describe('assetChanges（PATCH 只发送改动字段）', () => {
  it('未改动时为空对象', () => {
    expect(assetChanges(base, { ...base })).toEqual({});
  });
  it('只包含改动字段，从不包含 id/projectId/version', () => {
    const patch = assetChanges(base, { ...base, assetCode: 'CRN-NEW', version: 99, projectId: 'P002', id: 'x' });
    expect(patch).toEqual({ assetCode: 'CRN-NEW' });
  });
  it('清空可空字段发送 null', () => {
    expect(assetChanges(base, { ...base, serialNumber: null, liftingHeightM: null })).toEqual({ serialNumber: null, liftingHeightM: null });
  });
});
