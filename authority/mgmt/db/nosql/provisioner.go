package nosql

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"github.com/smallstep/certificates/authority/mgmt"
	"github.com/smallstep/nosql"
)

// dbProvisioner is the database representation of a Provisioner type.
type dbProvisioner struct {
	ID               string       `json:"id"`
	AuthorityID      string       `json:"authorityID"`
	Type             string       `json:"type"`
	Name             string       `json:"name"`
	Claims           *mgmt.Claims `json:"claims"`
	Details          []byte       `json:"details"`
	X509Template     string       `json:"x509Template"`
	X509TemplateData []byte       `json:"x509TemplateData"`
	SSHTemplate      string       `json:"sshTemplate"`
	SSHTemplateData  []byte       `json:"sshTemplateData"`
	CreatedAt        time.Time    `json:"createdAt"`
	DeletedAt        time.Time    `json:"deletedAt"`
}

func (dbp *dbProvisioner) clone() *dbProvisioner {
	u := *dbp
	return &u
}

func (db *DB) getDBProvisionerBytes(ctx context.Context, id string) ([]byte, error) {
	data, err := db.db.Get(authorityProvisionersTable, []byte(id))
	if nosql.IsErrNotFound(err) {
		return nil, mgmt.NewError(mgmt.ErrorNotFoundType, "provisioner %s not found", id)
	} else if err != nil {
		return nil, errors.Wrapf(err, "error loading provisioner %s", id)
	}
	return data, nil
}

func (db *DB) getDBProvisioner(ctx context.Context, id string) (*dbProvisioner, error) {
	data, err := db.getDBProvisionerBytes(ctx, id)
	if err != nil {
		return nil, err
	}
	dbp, err := unmarshalDBProvisioner(data, id)
	if err != nil {
		return nil, err
	}
	if dbp.AuthorityID != db.authorityID {
		return nil, mgmt.NewError(mgmt.ErrorAuthorityMismatchType,
			"provisioner %s is not owned by authority %s", dbp.ID, db.authorityID)
	}
	return dbp, nil
}

// GetProvisioner retrieves and unmarshals a provisioner from the database.
func (db *DB) GetProvisioner(ctx context.Context, id string) (*mgmt.Provisioner, error) {
	data, err := db.getDBProvisionerBytes(ctx, id)
	if err != nil {
		return nil, err
	}

	prov, err := unmarshalProvisioner(data, id)
	if err != nil {
		return nil, err
	}
	if prov.Status == mgmt.StatusDeleted {
		return nil, mgmt.NewError(mgmt.ErrorDeletedType, "provisioner %s is deleted", prov.ID)
	}
	if prov.AuthorityID != db.authorityID {
		return nil, mgmt.NewError(mgmt.ErrorAuthorityMismatchType,
			"provisioner %s is not owned by authority %s", prov.ID, db.authorityID)
	}
	return prov, nil
}

func unmarshalDBProvisioner(data []byte, id string) (*dbProvisioner, error) {
	var dbp = new(dbProvisioner)
	if err := json.Unmarshal(data, dbp); err != nil {
		return nil, errors.Wrapf(err, "error unmarshaling provisioner %s into dbProvisioner", id)
	}
	return dbp, nil
}

type detailsType struct {
	Type mgmt.ProvisionerType
}

func unmarshalProvisioner(data []byte, id string) (*mgmt.Provisioner, error) {
	dbp, err := unmarshalDBProvisioner(data, id)
	if err != nil {
		return nil, err
	}

	dt := new(detailsType)
	if err := json.Unmarshal(dbp.Details, dt); err != nil {
		return nil, mgmt.WrapErrorISE(err, "error unmarshaling details to detailsType for provisioner %s", id)
	}
	details, err := unmarshalDetails(dt.Type, dbp.Details)
	if err != nil {
		return nil, err
	}

	prov := &mgmt.Provisioner{
		ID:               dbp.ID,
		AuthorityID:      dbp.AuthorityID,
		Type:             dbp.Type,
		Name:             dbp.Name,
		Claims:           dbp.Claims,
		Details:          details,
		X509Template:     dbp.X509Template,
		X509TemplateData: dbp.X509TemplateData,
		SSHTemplate:      dbp.SSHTemplate,
		SSHTemplateData:  dbp.SSHTemplateData,
	}
	if !dbp.DeletedAt.IsZero() {
		prov.Status = mgmt.StatusDeleted
	}
	return prov, nil
}

// GetProvisioners retrieves and unmarshals all active (not deleted) provisioners
// from the database.
// TODO should we be paginating?
func (db *DB) GetProvisioners(ctx context.Context) ([]*mgmt.Provisioner, error) {
	dbEntries, err := db.db.List(authorityProvisionersTable)
	if err != nil {
		return nil, errors.Wrap(err, "error loading provisioners")
	}
	var provs []*mgmt.Provisioner
	for _, entry := range dbEntries {
		prov, err := unmarshalProvisioner(entry.Value, string(entry.Key))
		if err != nil {
			return nil, err
		}
		if prov.Status == mgmt.StatusDeleted {
			continue
		}
		if prov.AuthorityID != db.authorityID {
			continue
		}
		provs = append(provs, prov)
	}
	return provs, nil
}

// CreateProvisioner stores a new provisioner to the database.
func (db *DB) CreateProvisioner(ctx context.Context, prov *mgmt.Provisioner) error {
	var err error
	prov.ID, err = randID()
	if err != nil {
		return errors.Wrap(err, "error generating random id for provisioner")
	}

	dbp := &dbProvisioner{
		ID:           prov.ID,
		AuthorityID:  db.authorityID,
		Type:         prov.Type,
		Name:         prov.Name,
		Claims:       prov.Claims,
		Details:      prov.Details,
		X509Template: prov.X509Template,
		SSHTemplate:  prov.SSHTemplate,
		CreatedAt:    clock.Now(),
	}

	return db.save(ctx, dbp.ID, dbp, nil, "provisioner", authorityProvisionersTable)
}

// UpdateProvisioner saves an updated provisioner to the database.
func (db *DB) UpdateProvisioner(ctx context.Context, prov *mgmt.Provisioner) error {
	old, err := db.getDBProvisioner(ctx, prov.ID)
	if err != nil {
		return err
	}

	nu := old.clone()

	// If the provisioner was active but is now deleted ...
	if old.DeletedAt.IsZero() && prov.Status == mgmt.StatusDeleted {
		nu.DeletedAt = clock.Now()
	}
	nu.Claims = prov.Claims
	nu.Details = prov.Details
	nu.X509Template = prov.X509Template
	nu.SSHTemplate = prov.SSHTemplate

	return db.save(ctx, old.ID, nu, old, "provisioner", authorityProvisionersTable)
}

func unmarshalDetails(typ ProvisionerType, details []byte) (interface{}, error) {
	if !s.Valid {
		return nil, nil
	}
	var v isProvisionerDetails_Data
	switch typ {
	case ProvisionerTypeJWK:
		p := new(ProvisionerDetailsJWK)
		if err := json.Unmarshal([]byte(s.String), p); err != nil {
			return nil, err
		}
		if p.JWK.Key.Key == nil {
			key, err := LoadKey(ctx, db, p.JWK.Key.Id.Id)
			if err != nil {
				return nil, err
			}
			p.JWK.Key = key
		}
		return &ProvisionerDetails{Data: p}, nil
	case ProvisionerType_OIDC:
		v = new(ProvisionerDetails_OIDC)
	case ProvisionerType_GCP:
		v = new(ProvisionerDetails_GCP)
	case ProvisionerType_AWS:
		v = new(ProvisionerDetails_AWS)
	case ProvisionerType_AZURE:
		v = new(ProvisionerDetails_Azure)
	case ProvisionerType_ACME:
		v = new(ProvisionerDetails_ACME)
	case ProvisionerType_X5C:
		p := new(ProvisionerDetails_X5C)
		if err := json.Unmarshal([]byte(s.String), p); err != nil {
			return nil, err
		}
		for _, k := range p.X5C.GetRoots() {
			if err := k.Select(ctx, db, k.Id.Id); err != nil {
				return nil, err
			}
		}
		return &ProvisionerDetails{Data: p}, nil
	case ProvisionerType_K8SSA:
		p := new(ProvisionerDetails_K8SSA)
		if err := json.Unmarshal([]byte(s.String), p); err != nil {
			return nil, err
		}
		for _, k := range p.K8SSA.GetPublicKeys() {
			if err := k.Select(ctx, db, k.Id.Id); err != nil {
				return nil, err
			}
		}
		return &ProvisionerDetails{Data: p}, nil
	case ProvisionerType_SSHPOP:
		v = new(ProvisionerDetails_SSHPOP)
	default:
		return nil, fmt.Errorf("unsupported provisioner type %s", typ)
	}

	if err := json.Unmarshal([]byte(s.String), v); err != nil {
		return nil, err
	}
	return &ProvisionerDetails{Data: v}, nil
}